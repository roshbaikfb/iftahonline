import { useEffect, useId, useRef, type ReactNode, type SubmitEvent } from 'react';

interface Props {
  title: string;
  intro?: ReactNode;
  error?: string;
  className?: string;
  onSubmit: (e: SubmitEvent<HTMLFormElement>) => void;
  onClose: () => void;
  children: ReactNode;
}

// Dialog is the console's modal form: it takes the focus to its first
// field, closes on Escape or a click outside, and gives the focus back.
export function Dialog({ title, intro, error, className, onSubmit, onClose, children }: Props) {
  const box = useRef<HTMLFormElement>(null);
  const close = useRef(onClose);
  const titleId = useId();
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const back = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLElement>('input, select')?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close.current();
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      back?.focus();
    };
  }, []);

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form ref={box} className={`modal console-dialog ${className ?? ''}`} role="dialog" aria-modal="true" aria-labelledby={titleId} onSubmit={onSubmit}>
        <h2 id={titleId}>{title}</h2>
        {intro && <p className="muted">{intro}</p>}
        {error && <p role="alert">{error}</p>}
        {children}
      </form>
    </div>
  );
}
