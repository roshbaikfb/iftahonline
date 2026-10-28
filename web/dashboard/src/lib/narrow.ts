import { useEffect, useState } from 'react';

// The width below which screens take their phone layout (styles.css).
const query = '(max-width: 40rem)';

const media = () => (typeof globalThis.matchMedia === 'function' ? globalThis.matchMedia(query) : undefined);

// useNarrow says whether the screen is phone-sized, for layouts that
// differ in structure, not only in style (the home editor's sections).
export function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() => media()?.matches ?? false);
  useEffect(() => {
    const m = media();
    if (!m) return;
    const on = () => {
      setNarrow(m.matches);
    };
    m.addEventListener('change', on);
    return () => {
      m.removeEventListener('change', on);
    };
  }, []);
  return narrow;
}
