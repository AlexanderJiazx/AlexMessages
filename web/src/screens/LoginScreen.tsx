import { useState, type FormEvent } from "react";
import { api, store } from "../client";

/**
 * Sign-in / register card — the same markup and styling hooks as the legacy
 * login.html (`.card`, `.brand`, `.tabs`, `.submit`, `.msg`).
 */
export function LoginScreen({ onLogin }: { onLogin: () => void }) {
  const [tab, setTab] = useState<"login" | "register">("login");
  const [msg, setMsg] = useState<{ text: string; kind: "err" | "ok" } | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleLogin(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.login(String(fd.get("username") || ""), String(fd.get("password") || ""));
      store.state.me = r.user;
      onLogin();
    } catch (err) {
      setMsg({ text: (err as { detail?: string }).detail || "Sign-in failed", kind: "err" });
    } finally {
      setBusy(false);
    }
  }

  async function onRegister(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const fd = new FormData(form);
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.register(
        String(fd.get("username") || ""),
        String(fd.get("password") || ""),
        String(fd.get("display_name") || "") || undefined,
      );
      setMsg({
        text: r.message || "Account requested. Awaiting admin approval.",
        kind: "ok",
      });
      form.reset();
    } catch (err) {
      setMsg({ text: (err as { detail?: string }).detail || "Registration failed", kind: "err" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-screen">
      <div className="card login-card">
        <div className="brand">
          <div className="logo" />
          <div>
            <h1 className="serif">Alex Messages</h1>
            <div className="net">
              <span className="dot" /> {location.host}
            </div>
          </div>
        </div>

        <div className="tabs" role="tablist">
          <button
            className={tab === "login" ? "active" : ""}
            type="button"
            onClick={() => {
              setTab("login");
              setMsg(null);
            }}
          >
            Sign in
          </button>
          <button
            className={tab === "register" ? "active" : ""}
            type="button"
            onClick={() => {
              setTab("register");
              setMsg(null);
            }}
          >
            Register
          </button>
        </div>

        {tab === "login" ? (
          <form onSubmit={handleLogin}>
            <label>
              Username
              <input name="username" autoComplete="username" required autoFocus />
            </label>
            <label>
              Password
              <input name="password" type="password" autoComplete="current-password" required />
            </label>
            <button className="submit" type="submit" disabled={busy}>
              Sign in
            </button>
          </form>
        ) : (
          <form onSubmit={onRegister}>
            <label>
              Username
              <input name="username" autoComplete="username" required minLength={3} maxLength={32} />
            </label>
            <label>
              Display name
              <input
                name="display_name"
                maxLength={40}
                placeholder="Optional — defaults to your username"
              />
            </label>
            <label>
              Password
              <input name="password" type="password" autoComplete="new-password" required minLength={8} />
            </label>
            <button className="submit" type="submit" disabled={busy}>
              Request account
            </button>
            <div className="hint">
              New accounts are reviewed by an administrator. You'll be able to sign in once
              approved.
            </div>
          </form>
        )}

        {msg && <div className={`msg ${msg.kind}`}>{msg.text}</div>}
      </div>
    </div>
  );
}
