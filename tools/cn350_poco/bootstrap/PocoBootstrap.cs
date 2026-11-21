// 独立测试程序集，不能编入/覆盖 Assembly-CSharp.dll。
using UnityEngine;
using System;
using System.Reflection;
namespace Cn350Test {
    public static class PocoBootstrap {
        public static void Start() {
            const string name = "__CN350_Poco_TestBridge";
            if (GameObject.Find(name) != null) return;
            GameObject bridge = new GameObject(name);
            UnityEngine.Object.DontDestroyOnLoad(bridge);
            PocoManager manager = bridge.AddComponent<PocoManager>();
            // 官方服务遇端口占用可能自动尝试5002等，不能误连另一进程的5001。
            FieldInfo field = typeof(PocoManager).GetField("server", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
            object server = field == null ? null : field.GetValue(manager);
            PropertyInfo port = server == null ? null : server.GetType().GetProperty("Port");
            if (port == null || Convert.ToInt32(port.GetValue(server, null)) != 5001) {
                UnityEngine.Object.Destroy(bridge);
                throw new InvalidOperationException("Poco SDK server schema/port incompatible; refuse fallback port");
            }
        }
    }
}
