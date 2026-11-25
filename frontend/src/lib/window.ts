import type { CSSProperties } from "react";
import { Window } from "@wailsio/runtime";

// Window chrome helpers. The app runs frameless, so these stand in for the
// caption buttons the OS would normally draw.

export const minimise = () => void Window.Minimise().catch(() => {});

export const toggleMaximise = async (): Promise<boolean> => {
  try {
    await Window.ToggleMaximise();
    return await Window.IsMaximised();
  } catch {
    return false;
  }
};

export const isMaximised = async (): Promise<boolean> => {
  try {
    return await Window.IsMaximised();
  } catch {
    return false;
  }
};

// Style helper: mark an element as part of the drag region or explicitly not.
export const drag = { "--app-region": "drag" } as CSSProperties;
export const noDrag = { "--app-region": "none" } as CSSProperties;

