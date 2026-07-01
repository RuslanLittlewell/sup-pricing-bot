import * as React from "react"

interface Props {
  items: string[]
  className?: string
  itemClassName?: string
  showGradients?: boolean
  displayScrollbar?: boolean
}

export default function AnimatedList({
  items,
  className = "",
  itemClassName = "",
  showGradients = true,
  displayScrollbar = false,
}: Props) {
  const listRef = React.useRef<HTMLDivElement>(null)
  const itemRefs = React.useRef<Array<HTMLDivElement | null>>([])
  const [visible, setVisible] = React.useState<boolean[]>(() => items.map(() => false))
  const [topOpacity, setTopOpacity] = React.useState(0)
  const [bottomOpacity, setBottomOpacity] = React.useState(1)

  const updateGradients = React.useCallback((el: HTMLDivElement) => {
    const { scrollTop, scrollHeight, clientHeight } = el
    setTopOpacity(Math.min(scrollTop / 40, 1))
    const distanceFromBottom = scrollHeight - (scrollTop + clientHeight)
    setBottomOpacity(scrollHeight <= clientHeight ? 0 : Math.min(distanceFromBottom / 40, 1))
  }, [])

  React.useEffect(() => {
    const container = listRef.current
    if (!container) return
    updateGradients(container)

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue
          const index = itemRefs.current.indexOf(entry.target as HTMLDivElement)
          if (index === -1) continue
          setVisible((prev) => {
            if (prev[index]) return prev
            const next = [...prev]
            next[index] = true
            return next
          })
        }
      },
      { root: container, threshold: 0.3 },
    )

    itemRefs.current.forEach((el) => el && observer.observe(el))
    return () => observer.disconnect()
  }, [items, updateGradients])

  return (
    <div className={`relative ${className}`}>
      <div
        ref={listRef}
        onScroll={(e) => updateGradients(e.currentTarget)}
        className={`flex h-full flex-col gap-2.5 overflow-y-auto ${displayScrollbar ? "" : "no-scrollbar"}`}
      >
        {items.map((item, i) => (
          <div
            key={item}
            ref={(el) => {
              itemRefs.current[i] = el
            }}
            className={`transition-all ease-out ${
              visible[i] ? "translate-y-0 scale-100 opacity-100 duration-500" : "translate-y-3 scale-95 opacity-0 duration-0"
            } ${itemClassName}`}
            style={{ transitionDelay: visible[i] ? `${i * 45}ms` : "0ms" }}
          >
            {item}
          </div>
        ))}
      </div>

      {showGradients && (
        <>
          <div
            className="pointer-events-none absolute inset-x-0 top-0 h-10 bg-gradient-to-b from-[#58acff] to-transparent transition-opacity"
            style={{ opacity: topOpacity }}
          />
          <div
            className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-[#58acff] to-transparent transition-opacity"
            style={{ opacity: bottomOpacity }}
          />
        </>
      )}
    </div>
  )
}
