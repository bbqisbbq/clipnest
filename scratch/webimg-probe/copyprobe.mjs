// CDP driver for the clipboard-format probe.
//
//   node copyprobe.mjs select        → select the <img>, then fire copy (Ctrl+C path)
//   node copyprobe.mjs contextmenu   → right-click the image (native menu opens; pick item with the keyboard)
//   node copyprobe.mjs copyaddr      → invoke the "copy image address" style action (URL only)
//
// It talks to Chrome/Edge over the DevTools protocol on port 9333.

const PORT = 9333;
const mode = process.argv[2] ?? "select";

async function pickPage() {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === "page" && t.url.includes("test.html"));
  if (!page) throw new Error("probe page not found");
  return page;
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    let id = 0;
    const pending = new Map();
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && pending.has(msg.id)) {
        const { resolve: res, reject: rej } = pending.get(msg.id);
        pending.delete(msg.id);
        msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
      }
    };
    ws.onerror = (e) => reject(e);
    ws.onopen = () =>
      resolve({
        send(method, params = {}) {
          const msgId = ++id;
          return new Promise((res, rej) => {
            pending.set(msgId, { resolve: res, reject: rej });
            ws.send(JSON.stringify({ id: msgId, method, params }));
          });
        },
        close: () => ws.close(),
      });
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const page = await pickPage();
const cdp = await connect(page.webSocketDebuggerUrl);
await cdp.send("Page.enable");
await cdp.send("Runtime.enable");
// 把页面拿到前台：Chromium 的复制命令要求目标处于激活状态，否则 Ctrl+C 什么也不写
// （表现为"剪贴板根本没变"，容易误判成 ClipNest 没采到）。
await cdp.send("Page.bringToFront").catch(() => {});
await sleep(300);

const box = await cdp.send("Runtime.evaluate", {
  expression: `(() => { const r = document.getElementById('pic').getBoundingClientRect();
    return {x: r.x + r.width/2, y: r.y + r.height/2, w: r.width, h: r.height}; })()`,
  returnByValue: true,
});
const { x, y } = box.result.value;
console.log(`image center: ${x},${y}`);

if (mode === "select") {
  // Select the image node (what a mouse selection + Ctrl+C would copy).
  const sel = await cdp.send("Runtime.evaluate", {
    expression: `(() => { const img = document.getElementById('pic');
      const s = getSelection(); s.removeAllRanges();
      const r = document.createRange(); r.selectNode(img); s.addRange(r);
      return String(s); })()`,
    returnByValue: true,
  });
  console.log("selection:", JSON.stringify(sel.result.value));
  // A real Ctrl+C through the input pipeline (not execCommand) so the browser
  // uses its normal copy path including image handling.
  await cdp.send("Input.dispatchKeyEvent", { type: "rawKeyDown", modifiers: 2, windowsVirtualKeyCode: 67, code: "KeyC", key: "c" });
  await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", modifiers: 2, windowsVirtualKeyCode: 67, code: "KeyC", key: "c" });
} else if (mode === "contextmenu") {
  await cdp.send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "right", clickCount: 1, buttons: 2 });
  await cdp.send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "right", clickCount: 1, buttons: 0 });
  console.log("right-click dispatched — the native context menu should be open now");
} else if (mode === "copyaddr") {
  await cdp.send("Runtime.evaluate", {
    expression: `navigator.clipboard.writeText(document.getElementById('pic').src)`,
    awaitPromise: true,
  });
  console.log("wrote the image url via navigator.clipboard");
}

await sleep(500);
cdp.close();
