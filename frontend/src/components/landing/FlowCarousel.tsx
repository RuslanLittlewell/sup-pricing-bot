import * as React from "react"
import Autoplay from "embla-carousel-autoplay"
import {
  Carousel,
  CarouselContent,
  CarouselItem,
  type CarouselApi,
} from "@/components/ui/carousel"

interface Props {
  slides: string[]
  /** "dark" (default) — white dots, for use on gradient/dark backgrounds; "light" — dark dots, for use on white backgrounds */
  variant?: "dark" | "light"
}

export default function FlowCarousel({ slides, variant = "dark" }: Props) {
  const [api, setApi] = React.useState<CarouselApi>()
  const [current, setCurrent] = React.useState(0)

  const plugin = React.useRef(Autoplay({ delay: 2500, stopOnInteraction: false }))

  React.useEffect(() => {
    if (!api) return
    setCurrent(api.selectedScrollSnap())
    api.on("select", () => setCurrent(api.selectedScrollSnap()))
  }, [api])

  return (
    <div className="flex flex-col items-center gap-4">
      <div className="w-full overflow-hidden rounded-[2.5rem]">
        <Carousel
          setApi={setApi}
          plugins={[plugin.current]}
          opts={{ loop: true }}
          className="h-[500px] w-full"
        >
          <CarouselContent className="-ml-0 w-full h-[500px]">
            {slides.map((src, i) => (
              <CarouselItem key={i} className="pl-0 w-full h-[500px]">
                <img
                  src={src}
                  alt={`SurPrice Bot step ${i + 1}`}
                  loading={i === 0 ? "eager" : "lazy"}
                  decoding="async"
                  className="h-[500px] w-auto object-cover mx-auto"
                />
              </CarouselItem>
            ))}
          </CarouselContent>
        </Carousel>
      </div>

      {/* Dot indicators */}
      <div className="flex gap-2">
        {slides.map((_, i) => (
          <button
            key={i}
            onClick={() => api?.scrollTo(i)}
            className={`h-1.5 rounded-full transition-all duration-300 ${
              variant === "light"
                ? i === current
                  ? "w-4 bg-slate-900"
                  : "w-1.5 bg-slate-900/25"
                : i === current
                  ? "w-4 bg-white"
                  : "w-1.5 bg-white/40"
            }`}
            aria-label={`Go to slide ${i + 1}`}
          />
        ))}
      </div>
    </div>
  )
}
