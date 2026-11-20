import { useEffect, useRef } from "react";

export type ToastKind = "ok" | "error" | "info";

export type ToastState = { message: string; kind: ToastKind };

export default function AdminToast({ toast, onClose }: {
  toast: ToastState | null;
  onClose: () => void;
}) {
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => closeRef.current(), 4200);
    return () => window.clearTimeout(timer);
  }, [toast]);

  if (!toast) return null;
  return (
    <div className={`cs-toast cs-toast--${toast.kind}`} role="status">
      <span>{toast.message}</span>
      <button type="button" onClick={onClose} aria-label="关闭">×</button>
    </div>
  );
}