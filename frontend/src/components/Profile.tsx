import { useEffect, useState } from "react";
import { authClient } from "../lib/auth-client";
import { api } from "../lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function Profile() {
  const [email, setEmail] = useState("");
  const [tgLinked, setTgLinked] = useState(false);
  const [tgInfo, setTgInfo] = useState("");
  const [linkCode, setLinkCode] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    (async () => {
      const session = await authClient.getSession();
      if (!session.data) { window.location.href = "/login"; return; }
      setEmail(session.data.user.email || "");

      try {
        const info = await api("/api/telegram/link");
        if (info.telegram_id) {
          setTgLinked(true);
          setTgInfo(info.telegram_username || `ID: ${info.telegram_id}`);
        }
      } catch {}
    })();
  }, []);

  const handleLink = async () => {
    try {
      const data = await api("/api/telegram/link-code", { method: "POST" });
      setLinkCode(data.code);
    } catch (e: any) {
      setError(e.message || "Ошибка");
    }
  };

  const handleLogout = async () => {
    await authClient.signOut();
    window.location.href = "/login";
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Профиль</CardTitle>
          <CardDescription>Ваш email и данные аккаунта</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm"><span className="font-medium">Email:</span> {email}</p>
          <Button variant="destructive" onClick={handleLogout}>Выйти</Button>
        </CardContent>
      </Card>

      <h2 className="text-xl font-semibold mt-8 mb-4">Привязка Telegram</h2>
      <Card>
        <CardContent className="pt-6">
          {tgLinked ? (
            <p className="text-sm">✅ Telegram привязан: {tgInfo}</p>
          ) : (
            <div className="space-y-3">
              <p className="text-sm text-muted-foreground">
                Привяжите Telegram для получения уведомлений и управления трекерами через бота.
              </p>
              {!linkCode ? (
                <Button onClick={handleLink}>Привязать Telegram</Button>
              ) : (
                <div className="rounded-lg bg-muted p-4 text-center space-y-3">
                  <p className="text-sm">Отправьте этот код боту:</p>
                  <p className="text-3xl font-bold tracking-[0.25em] font-mono">{linkCode}</p>
                  <p className="text-xs text-muted-foreground">Код действителен 5 минут</p>
                  <a href="https://t.me/sur_price_bot" target="_blank" rel="noopener noreferrer">
                    <Button variant="secondary">Открыть бота</Button>
                  </a>
                </div>
              )}
            </div>
          )}
          {error && <p className="text-sm text-destructive mt-3">{error}</p>}
        </CardContent>
      </Card>
    </>
  );
}
