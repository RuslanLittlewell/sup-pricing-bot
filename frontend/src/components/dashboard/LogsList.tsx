import { useEffect, useState } from "react";
import { authClient } from "../../lib/auth-client";
import { api, isAuthError } from "../../lib/api";
import { Card, CardContent } from "@/components/ui/card";

const labels: Record<string, string> = {
  in_stock: "В наличии", out_of_stock: "Нет в наличии", unknown: "Неизвестно",
  price_changed: "Цена изменилась", back_in_stock: "Появился в наличии",
  stock_changed: "Наличие изменилось",
  pending: "Ожидает", sent: "Отправлено", failed: "Ошибка",
};

interface LogEntry {
  id: string; tracker_id: string; type: string;
  old_price?: number; new_price?: number; currency?: string;
  old_stock_status?: string; new_stock_status?: string;
  status: string; created_at: string;
}

interface Tracker {
  id: string; title: string | null; domain: string;
}

export default function LogsList() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [trackers, setTrackers] = useState<Tracker[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [trackerFilter, setTrackerFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");

  useEffect(() => {
    (async () => {
      const session = await authClient.getSession();
      if (!session.data) { window.location.href = "/login"; return; }
      try {
        const [tData, nData] = await Promise.all([
          api("/api/trackers"),
          api("/api/notifications"),
        ]);
        setTrackers(tData);
        setLogs(nData || []);
      } catch (e) {
        if (isAuthError(e)) {
          window.location.href = "/login";
          return;
        }
        setError(e instanceof Error ? e.message : "Не удалось загрузить логи");
      }
      setLoading(false);
    })();
  }, []);

  const filtered = logs.filter(l => {
    if (trackerFilter && l.tracker_id !== trackerFilter) return false;
    if (typeFilter && l.type !== typeFilter) return false;
    return true;
  });

  return (
    <>
      <a href="/dashboard" className="text-sm text-muted-foreground hover:underline">← Назад</a>
      <h1 className="text-2xl font-bold my-4">Логи изменений</h1>

      <div className="flex gap-3 mb-4">
        <select
          value={trackerFilter}
          onChange={e => setTrackerFilter(e.target.value)}
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm max-w-[300px]"
        >
          <option value="">Все трекеры</option>
          {trackers.map(t => (
            <option key={t.id} value={t.id}>{t.title || t.domain}</option>
          ))}
        </select>
        <select
          value={typeFilter}
          onChange={e => setTypeFilter(e.target.value)}
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm max-w-[200px]"
        >
          <option value="">Все типы</option>
          <option value="price">Цена</option>
          <option value="stock">Наличие</option>
        </select>
      </div>

      {loading && <p className="text-muted-foreground">Загрузка...</p>}
      {error && <p className="text-sm text-destructive mb-4">{error}</p>}

      {!loading && !error && filtered.length === 0 && (
        <Card><CardContent className="py-8 text-center text-muted-foreground">Нет записей</CardContent></Card>
      )}

      {filtered.map(l => (
        <Card key={l.id} className="mb-2">
          <CardContent className="py-3">
            <div className="flex justify-between">
              <span className="font-medium text-sm">{labels[l.type] || l.type}</span>
              <span className="text-xs text-muted-foreground">{new Date(l.created_at).toLocaleString("ru-RU")}</span>
            </div>
            <div className="text-sm text-muted-foreground mt-1">
              {l.old_price != null && `${l.old_price} ${l.currency} → ${l.new_price} ${l.currency}`}
              {l.old_stock_status && `${labels[l.old_stock_status] || l.old_stock_status} → ${labels[l.new_stock_status] || l.new_stock_status}`}
            </div>
            <div className="text-xs text-muted-foreground mt-0.5">
              Статус: {labels[l.status] || l.status}
            </div>
          </CardContent>
        </Card>
      ))}
    </>
  );
}
