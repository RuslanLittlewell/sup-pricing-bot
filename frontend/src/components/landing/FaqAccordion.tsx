import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";

interface FaqItem {
  q: string;
  a: string;
}

interface Props {
  /** Already translated on the server for the page language. */
  items: FaqItem[];
}

export default function FaqAccordion({ items }: Props) {
  return (
    <Accordion type="single" collapsible className="flex flex-col gap-4">
      {items.map((item, i) => (
        <AccordionItem key={i} value={`item-${i}`}>
          <AccordionTrigger>{item.q}</AccordionTrigger>
          {/* forceMount keeps every answer in the server HTML (crawlers and AI
              assistants don't run JS); closed answers are hidden with CSS. */}
          <AccordionContent forceMount>{item.a}</AccordionContent>
        </AccordionItem>
      ))}
    </Accordion>
  );
}
