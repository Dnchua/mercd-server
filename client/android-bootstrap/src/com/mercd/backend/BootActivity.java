package com.mercd.backend;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.os.Bundle;
import android.os.Handler;
import android.view.Gravity;
import android.widget.TextView;

/** 只负责等待预置，不加载 Unity，也不阻塞 Android 主线程。 */
public final class BootActivity extends Activity {
    private final Handler handler = new Handler();
    private TextView status;
    private final Runnable poll = new Runnable() {
        public void run() {
            if (isFinishing()) return;
            status.setText(BootProvider.progress);
            if (BootProvider.failure != null) {
                status.setText("资源准备失败，请检查剩余存储空间后重新启动。\n" + BootProvider.failure);
                return;
            }
            if (BootProvider.ready) {
                try {
                    String target = getPackageManager().getActivityInfo(getComponentName(),
                            PackageManager.GET_META_DATA).metaData.getString("mercd.gameActivity");
                    startActivity(new Intent().setClassName(BootActivity.this, target));
                    finish();
                } catch (Exception e) {
                    status.setText("无法启动游戏：" + e.getMessage());
                }
                return;
            }
            handler.postDelayed(this, 250);
        }
    };

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        status = new TextView(this);
        status.setGravity(Gravity.CENTER);
        status.setTextSize(20);
        setContentView(status);
        handler.post(poll);
    }

    @Override protected void onDestroy() {
        handler.removeCallbacks(poll);
        super.onDestroy();
    }
}
