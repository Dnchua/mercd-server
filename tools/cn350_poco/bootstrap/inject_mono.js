'use strict';
// 只加载独立测试程序集。无业务方法替换、无返回值改写、无游戏状态写入。
let listener = null, status = {state: 'idle'}, keep = [], payload = null, busy = false;
function exp(mod, name, ret, args) {
    return new NativeFunction(mod.getExportByName(name), ret, args);
}
function probe() {
    return {frida: Frida.version, pid: Process.id, arch: Process.arch,
        modules: Process.enumerateModules().map(m => ({name:m.name, path:m.path})),
        threads: Process.enumerateThreads().map(t => ({id:t.id, name:t.name || '', state:t.state}))};
}
function start(mod) {
    const imageOpen = exp(mod,'mono_image_open_from_data_full','pointer',['pointer','uint','int','pointer','int']);
    const assemblyLoad = exp(mod,'mono_assembly_load_from_full','pointer',['pointer','pointer','pointer','int']);
    const getImage = exp(mod,'mono_assembly_get_image','pointer',['pointer']);
    const klassFromName = exp(mod,'mono_class_from_name','pointer',['pointer','pointer','pointer']);
    const methodFromName = exp(mod,'mono_class_get_method_from_name','pointer',['pointer','pointer','int']);
    const invoke = exp(mod,'mono_runtime_invoke','pointer',['pointer','pointer','pointer','pointer']);
    let last = null;
    for (const dll of payload.assemblies) {
        const bytes = new Uint8Array(dll.bytes), buf = Memory.alloc(bytes.length), err = Memory.alloc(4);
        buf.writeByteArray(bytes.buffer); err.writeS32(0); keep.push(buf,err);
        const img = imageOpen(buf,bytes.length,1,err,0);
        if (img.isNull() || err.readS32() !== 0) throw new Error('mono image failure: '+dll.name+' code='+err.readS32());
        const file = Memory.allocUtf8String(dll.name); keep.push(file);
        const asm = assemblyLoad(img,file,err,0);
        if (asm.isNull() || err.readS32() !== 0) throw new Error('mono assembly failure: '+dll.name);
        last = getImage(asm);
    }
    const cls = klassFromName(last,Memory.allocUtf8String('Cn350Test'),Memory.allocUtf8String('PocoBootstrap'));
    if (cls.isNull()) throw new Error('最后一个程序集缺 Cn350Test.PocoBootstrap');
    const method = methodFromName(cls,Memory.allocUtf8String('Start'),0);
    if (method.isNull()) throw new Error('缺 PocoBootstrap.Start()');
    const exception = Memory.alloc(Process.pointerSize); exception.writePointer(ptr(0));
    invoke(method,ptr(0),ptr(0),exception);
    if (!exception.readPointer().isNull()) throw new Error('bootstrap managed exception，读取logcat；不重试');
}
rpc.exports = {
    probe,
    status() { return status; },
    arm(options) {
        if (status.state !== 'idle') throw new Error('重复注入被拒绝');
        const mod = Process.getModuleByName(options.mono_module);
        if (!Process.enumerateThreads().some(t=>t.id===options.main_thread_id)) throw new Error('指定Unity主线程不存在');
        if (!options.assemblies.length) throw new Error('程序集列表为空');
        payload=options; status={state:'waiting-main-thread'};
        listener = Interceptor.attach(mod.getExportByName('mono_runtime_invoke'), {
            onLeave() {
                if (busy || status.state !== 'waiting-main-thread' || Process.getCurrentThreadId() !== options.main_thread_id) return;
                busy=true;
                try { start(mod); status={state:'loaded', thread:Process.getCurrentThreadId()}; }
                catch(e) { status={state:'failed', error:String(e)}; }
                finally { busy=false; listener.detach(); }
            }
        });
        return status;
    },
    cancel() { if(listener) listener.detach(); status={state:'cancelled'}; }
};
