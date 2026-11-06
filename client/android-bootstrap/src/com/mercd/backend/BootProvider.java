package com.mercd.backend;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.content.res.AssetManager;
import android.database.Cursor;
import android.net.Uri;
import android.util.Log;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import org.json.JSONObject;

import mobile.Mobile;

// BootProvider 在 Application.onCreate 之前由系统自动实例化：
// 把 APK assets/backend/* 解压到 filesDir，清理外部存储残留的旧资源清单缓存，
// 然后 Mobile.start 拉起进程内 Go 服务（127.0.0.1:18091），游戏客户端经 gsp.txt 直连，
// 全部 .unity3d 资源直接从 APK 内 assets/unity/（StreamingAssets）读取。
public class BootProvider extends ContentProvider {
    private static final String TAG = "mercd-boot";
    private static final int PORT = 18091;
    private static final String MANIFEST_CACHE_KEY = "asset_info_android4_v2_short.json.encode";

    static volatile boolean ready;
    static volatile String failure;
    static volatile String progress = "正在启动单机服务…";

    @Override
    public boolean onCreate() {
        final Context ctx = getContext();
        new Thread(new Runnable() {
            public void run() { start(ctx); }
        }, "mercd-boot").start();
        return true;
    }

    private static void start(Context ctx) {
        try {
            File files = ctx.getFilesDir();
            // 解压只发生在安装/升级后首次启动：以 APK lastUpdateTime 为戳。
            long stamp = ctx.getPackageManager()
                    .getPackageInfo(ctx.getPackageName(), 0).lastUpdateTime;
            File marker = new File(files, "state/.backend_extracted");
            File manifestTxt = new File(files, "assets/cn350/serve_asset_info_android4_v2_short.txt");
            if (!(marker.exists() && readStamp(marker) == stamp && manifestTxt.isFile())) {
                progress = "正在准备服务端配置…";
                AssetManager am = ctx.getAssets();
                copyTree(am, "backend/cn350", new File(files, "assets/cn350"));
                copyTree(am, "backend/cards", new File(files, "assets/shared/cards"));
                cleanLegacyExtractedBundles(files);
                marker.getParentFile().mkdirs();
                FileOutputStream out = new FileOutputStream(marker);
                out.write(String.valueOf(stamp).getBytes("UTF-8"));
                out.close();
                Log.i(TAG, "backend assets extracted to " + files);
            } else {
                Log.i(TAG, "backend assets already extracted, skip");
            }

            // 清理外部存储与内部存储下可能残留的旧 asset_info_android4_v2_short.json.encode 缓存，
            // 防止带第 6 字段 MD5 的旧清单缓存遮蔽服务端新下发的 5 字段无 MD5 清单。
            cleanManifestCache(ctx.getExternalFilesDir(null));
            cleanManifestCache(files);

            progress = "正在启动单机服务…";
            File state = new File(files, "state");
            //noinspection ResultOfMethodCallIgnored
            state.mkdirs();
            ensureSeedDatabase(ctx.getAssets(), state);
            Mobile.start(state.getAbsolutePath(),
                    new File(files, "assets/cn350").getAbsolutePath(),
                    new File(files, "assets/shared/cards").getAbsolutePath(),
                    PORT);
            Log.i(TAG, "Mobile.start returned, status=" + Mobile.status());
            long deadline = android.os.SystemClock.elapsedRealtime() + 300000;
            while (!"running".equals(Mobile.status())) {
                if ("stopped".equals(Mobile.status()) || android.os.SystemClock.elapsedRealtime() > deadline) {
                    throw new java.io.IOException("单机服务未就绪：" + Mobile.lastError());
                }
                Thread.sleep(100);
            }
            ready = true;
        } catch (Throwable t) {
            failure = t.toString();
            Log.e(TAG, "backend bootstrap failed", t);
        }
    }

    // 首次安装（或库文件为空）时从 APK assets/backend/seed.sqlite3 预置已完成迁移与
    // 静态名册/卡池/资源首版发布的种子库，避免在 32 位 houdini/wazero 下冷启动跑数十条
    // SQL 迁移与上万次 INSERT 导致卡死闪退；已有存档在升级时原样保留，仅增量执行
    // cutoff_migration 之后的新迁移。
    private static void ensureSeedDatabase(AssetManager am, File state) throws Exception {
        File metaDst = new File(state, "seed_meta.json");
        String cutoff = "unknown";
        int migrationCount = 0;
        try {
            String metaRaw = readText(am.open("backend/seed_meta.json"));
            copyFile(new ByteArrayInputStream(metaRaw.getBytes("UTF-8")), metaDst);
            JSONObject meta = new JSONObject(metaRaw);
            cutoff = meta.optString("cutoff_migration", "unknown");
            migrationCount = meta.optInt("migration_count", 0);
        } catch (Throwable t) {
            Log.w(TAG, "seed_meta.json not found or unreadable", t);
        }

        File dbFile = new File(state, "mercd35.sqlite3");
        if (dbFile.isFile() && dbFile.length() > 0) {
            Log.i(TAG, "existing database found (" + dbFile.length()
                    + " bytes), preserving player state (apk seed cutoff_migration=" + cutoff + ")");
            return;
        }
        progress = "正在初始化单机数据库…";
        // 清理可能由异常中断残留的 WAL/SHM 尾文件，确保种子库干净就位。
        //noinspection ResultOfMethodCallIgnored
        new File(state, "mercd35.sqlite3-wal").delete();
        //noinspection ResultOfMethodCallIgnored
        new File(state, "mercd35.sqlite3-shm").delete();
        copyFile(am.open("backend/seed.sqlite3"), dbFile);
        Log.i(TAG, "seeded initial database from backend/seed.sqlite3 (" + dbFile.length()
                + " bytes, cutoff_migration=" + cutoff + ", migrations=" + migrationCount + ")");
    }

    private static void cleanManifestCache(File dir) {
        if (dir == null || !dir.isDirectory()) return;
        try {
            File cachedManifest = new File(dir, MANIFEST_CACHE_KEY);
            if (cachedManifest.exists() && cachedManifest.delete()) {
                Log.i(TAG, "removed stale manifest cache: " + cachedManifest);
            }
            File md5File = new File(dir, "local_md5_v3.json");
            if (!md5File.isFile()) return;
            try {
                JSONObject cache = new JSONObject(readText(new FileInputStream(md5File)));
                if (cache.has(MANIFEST_CACHE_KEY)) {
                    cache.remove(MANIFEST_CACHE_KEY);
                    copyFile(new ByteArrayInputStream(cache.toString().getBytes("UTF-8")), md5File);
                    Log.i(TAG, "removed manifest key from " + md5File);
                }
            } catch (org.json.JSONException malformed) {
                copyFile(new FileInputStream(md5File), new File(dir, "local_md5_v3.json.previous"));
                copyFile(new ByteArrayInputStream("{}".getBytes("UTF-8")), md5File);
            }
        } catch (Throwable t) {
            Log.w(TAG, "cleanManifestCache ignored error at " + dir, t);
        }
    }

    private static void cleanLegacyExtractedBundles(File files) {
        File[] children = files.listFiles();
        if (children != null) {
            for (File child : children) {
                String name = child.getName();
                if ("assets".equals(name) || "state".equals(name)
                        || "local_md5_v3.json".equals(name)
                        || "local_md5_v3.json.previous".equals(name)
                        || "app_conf.json.encode".equals(name)) {
                    continue;
                }
                deleteRecursively(child);
            }
        }
        deleteRecursively(new File(files, "state/remote_assets"));
        deleteRecursively(new File(files, "state/remote_assets_state.json"));
    }

    private static void deleteRecursively(File target) {
        if (target == null || !target.exists()) return;
        if (target.isDirectory()) {
            File[] children = target.listFiles();
            if (children != null) {
                for (File child : children) deleteRecursively(child);
            }
        }
        //noinspection ResultOfMethodCallIgnored
        target.delete();
    }

    private static long readStamp(File f) {
        try {
            FileInputStream in = new FileInputStream(f);
            byte[] buf = new byte[32];
            int n = in.read(buf);
            in.close();
            return Long.parseLong(new String(buf, 0, Math.max(n, 0), "UTF-8").trim());
        } catch (Throwable t) {
            return -1;
        }
    }

    private static String readText(InputStream input) throws Exception {
        try (InputStream in = input; ByteArrayOutputStream out = new ByteArrayOutputStream()) {
            byte[] buf = new byte[65536];
            int n;
            while ((n = in.read(buf)) != -1) out.write(buf, 0, n);
            return new String(out.toByteArray(), "UTF-8");
        }
    }

    private static void copyFile(InputStream input, File dst) throws Exception {
        File temp = new File(dst.getPath() + ".mercd-tmp");
        try (InputStream in = input) {
            File parent = dst.getParentFile();
            if (!parent.isDirectory() && !parent.mkdirs()) throw new java.io.IOException("无法创建 " + parent);
            try (FileOutputStream out = new FileOutputStream(temp)) {
                byte[] buf = new byte[65536];
                int n;
                while ((n = in.read(buf)) != -1) {
                    out.write(buf, 0, n);
                }
                out.getFD().sync();
            }
            if (!temp.renameTo(dst)) throw new java.io.IOException("无法替换 " + dst);
        } finally {
            if (temp.exists()) temp.delete();
        }
    }

    private static void copyTree(AssetManager am, String src, File dst) throws Exception {
        String[] children = am.list(src);
        if (children == null || children.length == 0) {
            copyFile(am.open(src), dst);
            return;
        }
        if (!dst.isDirectory() && !dst.mkdirs()) throw new java.io.IOException("无法创建 " + dst);
        for (String child : children) copyTree(am, src + "/" + child, new File(dst, child));
    }

    @Override public Cursor query(Uri uri, String[] p, String s, String[] a, String o) { return null; }
    @Override public String getType(Uri uri) { return null; }
    @Override public Uri insert(Uri uri, ContentValues v) { return null; }
    @Override public int delete(Uri uri, String s, String[] a) { return 0; }
    @Override public int update(Uri uri, ContentValues v, String s, String[] a) { return 0; }
}
