import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// formatDate renders an ISO timestamp (e.g. "2026-07-04T13:36:15Z") as "DD-MM-YYYY HH:mm"
// in the viewer's local timezone. Falls back to the raw string for anything that doesn't
// parse as a date, so a malformed value is at least visible instead of showing "Invalid
// Date".
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${pad(d.getDate())}-${pad(d.getMonth() + 1)}-${d.getFullYear()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
