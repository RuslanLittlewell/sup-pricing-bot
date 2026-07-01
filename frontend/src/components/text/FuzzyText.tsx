import React, { useEffect, useRef, useState } from 'react';

interface FuzzyTextProps {
  children?: React.ReactNode;
  /**
   * Plain-text alternative to `children`. Use this when rendering FuzzyText as a
   * client-hydrated island from an .astro file — Astro serializes slotted children
   * across the island boundary as an object rather than a plain string, which breaks
   * the canvas text rendering (renders "[object Object]"). `text` avoids that.
   */
  text?: string;
  i18nKey?: string;
  fontSize?: number | string;
  fontWeight?: string | number;
  fontFamily?: string;
  color?: string;
  baseIntensity?: number;
  hoverIntensity?: number;
  fuzzRange?: number;
  fps?: number;
  clickEffect?: boolean;
  className?: string;
}

declare global {
  interface Window {
    surpriceI18n?: {
      dictionary?: Record<string, string>;
    };
  }
}

const FuzzyText = ({
  children,
  text: textProp,
  i18nKey,
  fontSize = '1em',
  fontWeight = 700,
  fontFamily = 'inherit',
  color = '#0f172a',
  baseIntensity = 0.21,
  hoverIntensity = 0,
  fuzzRange = 19,
  fps = 120,
  clickEffect = true,
  className = '',
}: FuzzyTextProps) => {
  const fallbackText = textProp ?? React.Children.toArray(children).join('');
  const [text, setText] = useState(fallbackText);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const canvasRef = useRef<HTMLCanvasElement & { cleanupFuzzyText?: () => void }>(null);

  useEffect(() => {
    if (!i18nKey) return;

    const syncText = () => {
      const translated = window.surpriceI18n?.dictionary?.[i18nKey];
      setText(translated || fallbackText);
    };

    syncText();
    window.addEventListener('surprice-language-change', syncText);
    return () => window.removeEventListener('surprice-language-change', syncText);
  }, [fallbackText, i18nKey]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const updateLayout = () => setLayoutVersion((version) => version + 1);
    const observer = new ResizeObserver(updateLayout);

    if (canvas.parentElement) {
      observer.observe(canvas.parentElement);
    }

    window.addEventListener('resize', updateLayout);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', updateLayout);
    };
  }, []);

  useEffect(() => {
    let animationFrameId = 0;
    let isCancelled = false;
    let clickTimeoutId: ReturnType<typeof setTimeout>;
    const canvas = canvasRef.current;
    if (!canvas || !text) return;

    const init = async () => {
      const ctx = canvas.getContext('2d');
      if (!ctx) return;

      const computedStyle = window.getComputedStyle(canvas);
      const computedFontFamily = fontFamily === 'inherit' ? computedStyle.fontFamily || 'sans-serif' : fontFamily;
      const inheritedFontSize = parseFloat(computedStyle.fontSize);
      let numericFontSize = typeof fontSize === 'number' ? fontSize : inheritedFontSize;
      let fontSizeStr = `${numericFontSize}px`;

      if (typeof fontSize === 'string' && fontSize !== 'inherit' && fontSize !== '1em') {
        const measuringNode = document.createElement('span');
        measuringNode.style.position = 'absolute';
        measuringNode.style.visibility = 'hidden';
        measuringNode.style.fontSize = fontSize;
        measuringNode.textContent = text;
        (canvas.parentElement || document.body).appendChild(measuringNode);
        numericFontSize = parseFloat(window.getComputedStyle(measuringNode).fontSize);
        measuringNode.remove();
        fontSizeStr = `${numericFontSize}px`;
      }

      const fontString = `${fontWeight} ${fontSizeStr} ${computedFontFamily}`;

      try {
        await document.fonts.load(fontString);
      } catch {
        await document.fonts.ready;
      }

      if (isCancelled) return;

      const offscreen = document.createElement('canvas');
      const offCtx = offscreen.getContext('2d');
      if (!offCtx) return;

      offCtx.font = fontString;
      offCtx.textBaseline = 'alphabetic';

      const metrics = offCtx.measureText(text);
      const actualLeft = metrics.actualBoundingBoxLeft ?? 0;
      const actualRight = metrics.actualBoundingBoxRight ?? metrics.width;
      const actualAscent = metrics.actualBoundingBoxAscent ?? numericFontSize;
      const actualDescent = metrics.actualBoundingBoxDescent ?? numericFontSize * 0.2;
      const textBoundingWidth = Math.ceil(actualLeft + actualRight);
      const tightHeight = Math.ceil(actualAscent + actualDescent);
      const extraWidthBuffer = 10;
      const offscreenWidth = textBoundingWidth + extraWidthBuffer;
      const xOffset = extraWidthBuffer / 2;

      offscreen.width = offscreenWidth;
      offscreen.height = tightHeight;
      offCtx.font = fontString;
      offCtx.textBaseline = 'alphabetic';
      offCtx.fillStyle = color;
      offCtx.fillText(text, xOffset - actualLeft, actualAscent);

      const horizontalMargin = fuzzRange + 20;
      canvas.width = offscreenWidth + horizontalMargin * 2;
      canvas.height = tightHeight;
      canvas.style.width = `${canvas.width}px`;
      canvas.style.height = `${canvas.height}px`;
      ctx.translate(horizontalMargin, 0);

      let isClicking = false;
      let lastFrameTime = 0;
      const frameDuration = 1000 / fps;

      const run = (timestamp: number) => {
        if (isCancelled) return;

        if (timestamp - lastFrameTime < frameDuration) {
          animationFrameId = window.requestAnimationFrame(run);
          return;
        }

        lastFrameTime = timestamp;
        const intensity = isClicking ? 1 : baseIntensity;
        ctx.clearRect(
          -fuzzRange - 20,
          -fuzzRange - 10,
          offscreenWidth + 2 * (fuzzRange + 20),
          tightHeight + 2 * (fuzzRange + 10)
        );

        for (let row = 0; row < tightHeight; row++) {
          const dx = Math.floor(intensity * (Math.random() - 0.5) * fuzzRange);
          ctx.drawImage(offscreen, 0, row, offscreenWidth, 1, dx, row, offscreenWidth, 1);
        }

        animationFrameId = window.requestAnimationFrame(run);
      };

      const handleClick = () => {
        if (!clickEffect) return;
        isClicking = true;
        clearTimeout(clickTimeoutId);
        clickTimeoutId = setTimeout(() => {
          isClicking = false;
        }, 150);
      };

      canvas.addEventListener('click', handleClick);
      animationFrameId = window.requestAnimationFrame(run);

      canvas.cleanupFuzzyText = () => {
        window.cancelAnimationFrame(animationFrameId);
        clearTimeout(clickTimeoutId);
        canvas.removeEventListener('click', handleClick);
      };
    };

    init();

    return () => {
      isCancelled = true;
      window.cancelAnimationFrame(animationFrameId);
      clearTimeout(clickTimeoutId);
      canvas.cleanupFuzzyText?.();
    };
  }, [baseIntensity, clickEffect, color, fontFamily, fontSize, fontWeight, fps, fuzzRange, hoverIntensity, layoutVersion, text]);

  return (
    <canvas
      ref={canvasRef}
      aria-label={text}
      role="img"
      className={`inline-block max-w-full cursor-pointer align-baseline ${className}`}
    />
  );
};

export default FuzzyText;
