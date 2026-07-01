import { useState, useEffect } from "react";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";

interface FaqItem {
  qKey: string;
  q: string;
  aKey: string;
  a: string;
}

interface Props {
  items: FaqItem[];
}

declare global {
  interface Window {
    surpriceI18n?: { language: string; dictionary: Record<string, string> };
  }
}

export default function FaqAccordion({ items }: Props) {
  const [dict, setDict] = useState<Record<string, string>>({});

  useEffect(() => {
    if (window.surpriceI18n?.dictionary) {
      setDict(window.surpriceI18n.dictionary);
    }

    const handler = (e: Event) => {
      const detail = (e as CustomEvent<{ dictionary: Record<string, string> }>).detail;
      setDict(detail.dictionary);
    };

    window.addEventListener("surprice-language-change", handler);
    return () => window.removeEventListener("surprice-language-change", handler);
  }, []);

  const t = (key: string, fallback: string) => dict[key] ?? fallback;

  return (
    <Accordion type="single" collapsible className="flex flex-col gap-4">
      {items.map((item, i) => (
        <AccordionItem key={i} value={`item-${i}`}>
          <AccordionTrigger>{t(item.qKey, item.q)}</AccordionTrigger>
          <AccordionContent>{t(item.aKey, item.a)}</AccordionContent>
        </AccordionItem>
      ))}
    </Accordion>
  );
}
