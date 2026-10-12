const NATIVE_DRAG_ZONE_HEIGHT = 40;

type NativeMessageBridge = {
  postMessage: (message: string) => void;
};

function postWailsMessage(message: string): boolean {
  try {
    const chromeBridge = (
      window as unknown as {
        chrome?: { webview?: NativeMessageBridge };
      }
    ).chrome?.webview;
    if (chromeBridge) {
      chromeBridge.postMessage(message);
      return true;
    }
    const webkitBridge = (
      window as unknown as {
        webkit?: { messageHandlers?: { external?: NativeMessageBridge } };
      }
    ).webkit?.messageHandlers?.external;
    if (webkitBridge) {
      webkitBridge.postMessage(message);
      return true;
    }
  } catch {
    // A browser preview has no native message bridge; the page remains usable there.
  }
  return false;
}

function isInteractiveTarget(target: EventTarget | null): boolean {
  return (
    target instanceof Element &&
    !!target.closest(
      ".app-toolbar, button, a, input, select, textarea, [role='button'], [contenteditable='true'], [data-no-window-drag]",
    )
  );
}

function isDragZoneEvent(event: MouseEvent): boolean {
  return (
    event.clientY >= 0 &&
    event.clientY < NATIVE_DRAG_ZONE_HEIGHT &&
    !isInteractiveTarget(event.target)
  );
}

/**
 * Extends Wails' native titlebar drag protocol below the six-pixel AppKit strip.
 * Capture listeners make the top coordinate zone transparent to layout while preserving all controls and content clicks.
 */
export function installNativeWindowDrag(): () => void {
  if (!document.documentElement.classList.contains("native-mac")) {
    return () => undefined;
  }

  let armed = false;
  const onMouseDown = (event: MouseEvent) => {
    armed = event.button === 0 && event.detail === 1 && isDragZoneEvent(event);
  };
  const onMouseMove = (event: MouseEvent) => {
    if (!armed || !(event.buttons & 1)) return;
    armed = false;
    if (postWailsMessage("wails:drag")) {
      event.preventDefault();
      event.stopPropagation();
    }
  };
  const onMouseUp = () => {
    armed = false;
  };
  const onDoubleClick = (event: MouseEvent) => {
    if (event.button !== 0 || !isDragZoneEvent(event)) return;
    armed = false;
    if (postWailsMessage("wails:drag:doubleclick")) {
      event.preventDefault();
      event.stopPropagation();
    }
  };

  window.addEventListener("mousedown", onMouseDown, true);
  window.addEventListener("mousemove", onMouseMove, true);
  window.addEventListener("mouseup", onMouseUp, true);
  window.addEventListener("dblclick", onDoubleClick, true);

  return () => {
    window.removeEventListener("mousedown", onMouseDown, true);
    window.removeEventListener("mousemove", onMouseMove, true);
    window.removeEventListener("mouseup", onMouseUp, true);
    window.removeEventListener("dblclick", onDoubleClick, true);
  };
}
