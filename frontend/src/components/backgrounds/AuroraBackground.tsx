import type { CSSProperties } from 'react';

type AuroraTheme = 'light' | 'dark';

type Wander = 'aurora-wander-a' | 'aurora-wander-b' | 'aurora-wander-c';

interface AuroraBlob {
  id: string;
  color: string;
  size: number;
  top: string;
  left: string;
  wander: Wander;
  duration: number;
  delay: number;
}

// Ported from DozumiLanding's AuroraBackground (palette from BabyTracker's
// aurora-background.tsx). Pure CSS — no client JS needed, so render it without
// a client:* directive. Sizes are big on purpose: the blobs should wash over most
// of the section. Positions are percentages of the parent, and each blob gets its
// own wander path, speed and phase so the motion doesn't repeat in sync.
// Keyframes and .aurora-blob live in styles/globals.css.
const BLOBS: AuroraBlob[] = [
  { id: 'sky-1', color: '#6FCBFF', size: 1100, top: '-20%', left: '-15%', wander: 'aurora-wander-a', duration: 38000, delay: -6000 },
  { id: 'blue-1', color: '#2A5CC8', size: 1400, top: '10%', left: '55%', wander: 'aurora-wander-b', duration: 46000, delay: -18000 },
  { id: 'milk-1', color: '#FFF4DC', size: 980, top: '35%', left: '-20%', wander: 'aurora-wander-c', duration: 34000, delay: -12000 },
  { id: 'pink-1', color: '#F27BA5', size: 1250, top: '45%', left: '-5%', wander: 'aurora-wander-b', duration: 42000, delay: -24000 },
  { id: 'violet-1', color: '#8E5CF0', size: 1300, top: '60%', left: '60%', wander: 'aurora-wander-a', duration: 50000, delay: -3000 },
  { id: 'sky-2', color: '#6FCBFF', size: 820, top: '75%', left: '20%', wander: 'aurora-wander-c', duration: 30000, delay: -14000 },
  { id: 'pink-2', color: '#F27BA5', size: 760, top: '5%', left: '25%', wander: 'aurora-wander-a', duration: 33000, delay: -22000 },
];

// Light canvas needs softer blobs; on dark they read at full strength.
const OPACITY: Record<AuroraTheme, number> = {
  light: 0.6,
  dark: 1,
};

function blobGradient(color: string) {
  return `radial-gradient(circle closest-side, ${color}80 0%, ${color}33 55%, ${color}00 100%)`;
}

export default function AuroraBackground({ theme = 'dark' }: { theme?: AuroraTheme }) {
  return (
    <div
      aria-hidden="true"
      className="pointer-events-none absolute inset-0 overflow-hidden"
      style={{ opacity: OPACITY[theme] }}
    >
      {BLOBS.map((blob) => (
        <div
          key={blob.id}
          className="aurora-blob"
          style={
            {
              top: blob.top,
              left: blob.left,
              width: blob.size,
              height: blob.size,
              background: blobGradient(blob.color),
              '--aurora-anim': blob.wander,
              '--aurora-duration': `${blob.duration}ms`,
              '--aurora-delay': `${blob.delay}ms`,
            } as CSSProperties
          }
        />
      ))}
    </div>
  );
}
