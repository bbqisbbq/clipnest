// Wails 运行时（`@wailsio/runtime`）的极薄 shim：组件里的 `import { Events, Window }`
// 通过 vite/tsconfig 的别名指到这里，**组件代码一行都不用改**。
//
// 两处语义差异在这里抹平：
//   · Wails 的 Events.On(name, cb) 回调参数是 { name, data }，MyGo 直接给载荷。
//   · Wails 的窗口方法叫 Minimise/ToggleMaximise/IsMaximised（英式拼写）。
import { currentWindow, on } from "mygo-runtime";

export const Window = {
  Minimise: (): Promise<void> => currentWindow.minimize(),
  Maximise: (): Promise<void> => currentWindow.maximize(),
  UnMaximise: (): Promise<void> => currentWindow.unmaximize(),
  ToggleMaximise: (): Promise<void> => currentWindow.toggleMaximize(),
  IsMaximised: (): Promise<boolean> => currentWindow.isMaximized(),
  ToggleFullscreen: (): Promise<void> => currentWindow.toggleFullScreen(),
  Close: (): Promise<void> => currentWindow.close(),
  SetTitle: (title: string): Promise<void> => currentWindow.setTitle(title),
};

type Handler<T> = (ev: { name: string; data: T }) => void;

export const Events = {
  On<T = unknown>(name: string, cb: Handler<T>): () => void {
    return on<T>(name, (payload) => cb({ name, data: payload }));
  },
  Once<T = unknown>(name: string, cb: Handler<T>): () => void {
    let off: (() => void) | null = null;
    off = on<T>(name, (payload) => {
      off?.();
      cb({ name, data: payload });
    });
    return () => off?.();
  },
};
