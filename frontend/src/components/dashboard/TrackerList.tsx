import { useEffect, useState } from "react";
import { authClient } from "../../lib/auth-client";
import { api, isAuthError } from "../../lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

const labels: Record<string, string> = {
  active: "Активен", paused: "Приостановлен", needs_confirmation: "Требуется подтверждение",
  in_stock: "В наличии", out_of_stock: "Нет в наличии", unknown: "Неизвестно",
};

interface Tracker {
  id: string; url: string; domain: string; title: string | null;
  current_price: number | null; currency: string;
  current_stock_status: string; status: string;
  last_checked_at: string | null; last_error?: string;
}

export default function TrackerList() {
  const [trackers, setTrackers] = useState<Tracker[]>([]);
  const [loading, setLoading] = useState(true);
  const [userEmail, setUserEmail] = useState("");

  useEffect(() => {
    (async () => {
      const session = await authClient.getSession();
      if (session.data) {
        setUserEmail(session.data.user.email || "");
      }
      try {
        const data = await api("/api/trackers");
        setTrackers(data);
      } catch (e) {
        if (isAuthError(e)) {
          window.location.href = "/login";
        }
      }
      setLoading(false);
    })();
  }, []);

  const action = async (id: string, act: string) => {
    await api(`/api/trackers/${id}/${act}`, { method: "POST" });
    setTrackers(await api("/api/trackers"));
  };

  const del = async (id: string) => {
    if (!confirm("Удалить трекер?")) return;
    await api(`/api/trackers/${id}`, { method: "DELETE" });
    setTrackers(await api("/api/trackers"));
  };

  const handleLogout = async () => {
    await authClient.signOut();
    window.location.href = "/login";
  };

  return (
    <>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">Панель управления</h1>
          <p className="text-sm text-muted-foreground">{userEmail}</p>
        </div>
        <div className="flex gap-2">
          <a href="/profile"><Button variant="outline">Профиль</Button></a>
          <Button variant="destructive" onClick={handleLogout}>Выйти</Button>
        </div>
      </div>

      <div className="flex gap-4 mb-4">
        <a href="/dashboard"><Button variant="default">Трекеры</Button></a>
        <a href="/dashboard/logs"><Button variant="outline">Логи</Button></a>
      </div>

      {loading && <p className="text-muted-foreground">Загрузка...</p>}

      {!loading && trackers.length === 0 && (
        <Card>
          <CardContent className="py-12 text-center">
            <p className="text-muted-foreground mb-4">У вас пока нет трекеров</p>
            <p className="text-sm text-muted-foreground">Добавляйте трекеры через Telegram бота</p>
          </CardContent>
        </Card>
      )}

      {trackers.map(t => (
        <Card key={t.id} className="mb-3">
          <CardContent className="py-4">
            <div className="flex items-start justify-between">
              <div className="space-y-1">
                <a href={`/dashboard/trackers/${t.id}`} className="font-medium hover:underline">{t.title || t.domain}</a>
                <p className="text-sm text-muted-foreground break-all">{t.url}</p>
                <p className="text-sm">
                  <span className="font-semibold">{t.current_price ? `${t.current_price} ${t.currency}` : "—"}</span>
                  <span className="text-muted-foreground"> · {labels[t.current_stock_status] || t.current_stock_status}</span>
                  <span className="text-muted-foreground"> · {labels[t.status] || t.status}</span>
                </p>
              </div>
              <div className="flex gap-2 shrink-0">
                {t.status === "active" && <Button variant="secondary" size="sm" onClick={() => action(t.id, "pause")}>Пауза</Button>}
                {t.status === "paused" && <Button variant="secondary" size="sm" onClick={() => action(t.id, "resume")}>Возобновить</Button>}
                <Button variant="destructive" size="sm" onClick={() => del(t.id)}>✕</Button>
              </div>
            </div>
            <p className="text-xs text-muted-foreground mt-2">
              Последняя проверка: {t.last_checked_at ? new Date(t.last_checked_at).toLocaleString("ru-RU") : "—"}
              {t.last_error && <span className="text-destructive"> · Ошибка: {t.last_error}</span>}
            </p>
          </CardContent>
        </Card>
      ))}
    </>
  );
}
