import { useEffect, useState } from "react";
import { api, store } from "./client";
import { useChatState } from "./hooks";
import { LoginScreen } from "./screens/LoginScreen";
import { ChatScreen } from "./screens/ChatScreen";

type Phase = "boot" | "login" | "chat";

/**
 * Phase router: /api/me decides whether we show the login card or the chat
 * app. The WS init frame (store.ready) drives the loading state inside the
 * chat phase. 4401 closes and 401s route back to login.
 */
export function App() {
  const [phase, setPhase] = useState<Phase>("boot");
  const s = useChatState();

  useEffect(() => {
    api
      .getMe()
      .then((j) => {
        store.state.me = j.user;
        store.state.isAdmin = !!j.is_admin;
        store.state.meCreatedAt = j.created_at || 0;
        store.connect();
        setPhase("chat");
      })
      .catch(() => setPhase("login"));
  }, []);

  // When a session expires mid-use, the WS close(4401) navigates to /login
  // via onUnauthorized — but on first boot we still render the right phase.
  if (phase === "boot") {
    return (
      <div className="boot-splash">
        <div className="empty-logo" />
      </div>
    );
  }
  if (phase === "login")
    return (
      <LoginScreen
        onLogin={() => {
          store.connect();
          setPhase("chat");
        }}
      />
    );
  return <ChatScreen key={s.me?.id ?? 0} />;
}
