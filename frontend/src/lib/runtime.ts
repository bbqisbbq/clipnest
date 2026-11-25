// 页面运行时：组件只跟这里的 Events / Window 打交道，底下接的是 MyGo 的 mygo-runtime。
//
// 这层存在的原因：换壳前组件里写的是 `import { Events, Window } from "@wailsio/runtime"`，
// 而 Wails 和 MyGo 的两套 API 有两处形状差异，在这里一次性抹平，组件就不用动：
//   · Wails 的 Events.On(name, cb) 回调参数是 { name, data }，MyGo 直接给载荷。
//   · Wails 的窗口方法叫 Minimise / ToggleMaximise / IsMaximised（英式拼写）。
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
