"""按场景录制设备屏幕（补充证据；通过与否仍以 UI/请求/DB 三方门禁为准）。

默认使用 Airtest 的 yosemite 后端（清晰、文件较大）；`mode="ffmpeg"` 更省空间但画质低。
录像文件只落 run/（原始材料，不提交）；每次 stop 重写 `manifest.json`（case/路径/时长/大小/SHA-256）。
录制失败不会让用例失败，只在结果里带 `video_error`，避免录像设施影响验收判定。
"""
import contextlib
import hashlib
import json
import time
from pathlib import Path


class CaseRecorder:
    def __init__(self, device_getter, outdir, enabled=False, mode='yosemite', fps=10,
                 max_time=900, orientation=2, bit_rate=None, logger=print):
        self.device_getter = device_getter
        self.outdir = Path(outdir)
        self.enabled = bool(enabled)
        self.mode = mode
        self.fps = int(fps)
        self.max_time = int(max_time)
        self.orientation = orientation
        self.bit_rate = bit_rate
        self.logger = logger
        self.active = None
        self.entries = []
        self.last_error = None
        self.poll_attempts = 30
        self.poll_interval = 0.2

    # ---------- internal ----------
    def _device(self):
        try:
            return self.device_getter()
        except Exception as e:  # 设备未就绪/已断开
            self.last_error = '录制设备不可用: %s' % e
            return None

    def _write_manifest(self):
        if not self.enabled:
            return
        self.outdir.mkdir(parents=True, exist_ok=True)
        (self.outdir / 'manifest.json').write_text(
            json.dumps(self.entries, ensure_ascii=False, indent=2))

    def _append(self, entry):
        self.entries.append(entry)
        self._write_manifest()

    # ---------- public ----------
    def start(self, case_id):
        """开始录制一个 case；返回文件路径或 None。已在本 case 录制时先 stop。"""
        if not self.enabled:
            return None
        if self.active:
            self.stop()
        dev = self._device()
        if dev is None:
            return None
        self.outdir.mkdir(parents=True, exist_ok=True)
        path = self.outdir / ('%s-%s.mp4' % (case_id, time.strftime('%Y%m%d-%H%M%S')))
        opts = dict(max_time=self.max_time, output=str(path), fps=self.fps,
                    mode=self.mode, orientation=self.orientation)
        if self.bit_rate:
            opts['bit_rate'] = int(self.bit_rate)
        try:
            ret = dev.start_recording(**opts)
            # Airtest 在“已在录制”时返回 None；此时不再当成成功
            if ret is None:
                raise RuntimeError('start_recording 返回 None（可能已有录制在进行）')
            self.active = {'case': case_id, 'path': path, 'started': time.time(),
                           'start_epoch': time.time(), 'device': dev}
            self.last_error = None
            return path
        except Exception as e:
            self.last_error = '录制启动失败: %s' % e
            self.logger('[record] %s: %s' % (case_id, self.last_error))
            return None

    def stop(self):
        """停止并落盘当前 case 视频；返回 manifest 条目或 None。"""
        if not self.active:
            return None
        info, self.active = self.active, None
        dev = info.get('device') or self._device()
        try:
            if dev is not None:
                dev.stop_recording()
        except Exception as e:
            self.last_error = '录制停止失败: %s' % e
            self.logger('[record] %s: %s' % (info['case'], self.last_error))
        # yosemite 后端是异步落盘，轮询文件大小稳定
        path = info['path']
        size = 0
        for _ in range(self.poll_attempts):
            if path.is_file():
                size = path.stat().st_size
                if size > 0:
                    time.sleep(self.poll_interval)
                    if path.stat().st_size == size:
                        break
            time.sleep(self.poll_interval)
        entry = {'case': info['case'], 'video': str(path), 'bytes': size,
                 'duration_sec': round(time.time() - info['started'], 2),
                 'started_at': time.strftime('%Y-%m-%dT%H:%M:%S%z', time.localtime(info['start_epoch']))}
        if size > 0:
            entry['sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
        else:
            # 空文件不留在证据里
            try:
                path.unlink()
            except OSError:
                pass
            entry['error'] = '视频为空或落盘失败'
            self.last_error = entry['error']
        self._append(entry)
        return entry

    @contextlib.contextmanager
    def case(self, case_id):
        """脚本式用法：`with rec.case('G02'): ...` 自动起停并返回视频条目。"""
        self.start(case_id)
        box = {}
        try:
            yield box
        finally:
            box['video'] = self.stop()
