import { useState } from "react";
import { authClient } from "../lib/auth-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function RegisterForm() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleRegister = async () => {
    if (!name || !email || !password) { setError("Заполните все поля"); return; }
    setLoading(true); setError("");
    const { error: err } = await authClient.signUp.email({ name, email, password });
    if (err) { setError(err.message || err.statusText); setLoading(false); return; }
    window.location.href = "/dashboard";
  };

  return (
    <Card>
      <CardHeader className="text-center">
        <CardTitle className="text-2xl">Регистрация</CardTitle>
        <CardDescription>Создайте аккаунт для отслеживания цен</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="name">Имя</Label>
          <Input id="name" value={name} onChange={e => setName(e.target.value)} type="text" placeholder="Ваше имя" />
        </div>
        <div className="space-y-2">
          <Label htmlFor="email">Email</Label>
          <Input id="email" value={email} onChange={e => setEmail(e.target.value)} type="email" placeholder="your@email.com" />
        </div>
        <div className="space-y-2">
          <Label htmlFor="password">Пароль</Label>
          <Input id="password" value={password} onChange={e => setPassword(e.target.value)} type="password" placeholder="••••••••" />
        </div>
        <Button onClick={handleRegister} className="w-full" disabled={loading}>
          {loading ? "Регистрация..." : "Зарегистрироваться"}
        </Button>
        <p className="text-center text-sm text-muted-foreground">
          <a href="/login" className="underline underline-offset-4 hover:text-primary">Уже есть аккаунт? Войти</a>
        </p>
        {error && <p className="text-center text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
