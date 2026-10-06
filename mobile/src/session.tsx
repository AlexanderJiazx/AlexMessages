/**
 * Session — the mobile twin of the web client's client.ts glue.
 *
 * One ApiClient + one ChatStore for the whole app, created lazily once the
 * server URL and bearer token are known. Native differences from the web:
 *  - auth is a bearer token in SecureStore (no cookies),
 *  - notifications are local OS notifications via the store's onNotify hook,
 *  - AppState (not visibilitychange) drives store.setForeground.
 */
import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { AppState, Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import AsyncStorage from "@react-native-async-storage/async-storage";
import Constants from "expo-constants";
import {
  ApiClient,
  ChatStore,
  type ChatState,
  type NotifyEvent,
  type ToastEvent,
} from "@alexmessages/shared";
import { presentMessageNotification } from "./notify";

const TOKEN_KEY = "am_token";
const SERVER_KEY = "am_server_url";

const DEFAULT_SERVER: string =
  process.env.EXPO_PUBLIC_AM_SERVER ||
  (Constants.expoConfig?.extra as { defaultServerUrl?: string } | undefined)
    ?.defaultServerUrl ||
  "https://messages.alexanderjia.com";

export type Phase = "boot" | "login" | "ready";

interface Session {
  phase: Phase;
  api: ApiClient;
  store: ChatStore;
  serverUrl: string;
  login(username: string, password: string): Promise<string | null>;
  register(
    username: string,
    displayName: string,
    password: string
  ): Promise<{ ok: true } | { ok: false; error: string }>;
  logout(): Promise<void>;
  setServerUrl(url: string): Promise<void>;
}

const SessionCtx = createContext<Session | null>(null);

/** SecureStore is unavailable on web; fall back to AsyncStorage there. */
const kv = {
  get: (k: string): Promise<string | null> =>
    Platform.OS === "web" ? AsyncStorage.getItem(k) : SecureStore.getItemAsync(k),
  set: (k: string, v: string): Promise<void> =>
    Platform.OS === "web" ? AsyncStorage.setItem(k, v) : SecureStore.setItemAsync(k, v),
  del: (k: string): Promise<void> =>
    Platform.OS === "web" ? AsyncStorage.removeItem(k) : SecureStore.deleteItemAsync(k),
};

/** ChatStore's KeyValueStore is synchronous; AsyncStorage is async — so prefs
 *  live in a small in-memory map hydrated at boot and written through. */
const memPrefs = new Map<string, string>();
let prefsHydrated = false;
const prefStorage = {
  getItem: (k: string) => memPrefs.get(k) ?? null,
  setItem: (k: string, v: string) => {
    memPrefs.set(k, v);
    void AsyncStorage.setItem(`pref:${k}`, v);
  },
  removeItem: (k: string) => {
    memPrefs.delete(k);
    void AsyncStorage.removeItem(`pref:${k}`);
  },
};

async function hydratePrefs(): Promise<void> {
  if (prefsHydrated) return;
  prefsHydrated = true;
  try {
    const keys = await AsyncStorage.getAllKeys();
    const pairs = await AsyncStorage.multiGet(keys.filter((k) => k.startsWith("pref:")));
    for (const [k, v] of pairs) if (v != null) memPrefs.set(k.slice(5), v);
  } catch {
    /* non-fatal */
  }
}

function normalizeServerUrl(raw: string): string {
  let url = raw.trim();
  if (!url) return DEFAULT_SERVER;
  if (!/^https?:\/\//i.test(url)) url = `https://${url}`;
  url = url.replace(/\/+$/, "");
  // Android emulators can't reach the host's loopback directly — the
  // emulator alias 10.0.2.2 maps to the host machine's localhost.
  if (Platform.OS === "android" && !Constants.isDevice) {
    url = url.replace(/:\/\/(localhost|127\.0\.0\.1)(?=[:/]|$)/, "://10.0.2.2");
  }
  return url;
}

function wsUrlFor(baseUrl: string, token: string | null): string {
  const u = new URL(`${baseUrl}/ws`);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  if (token) u.searchParams.set("token", token);
  return u.toString();
}

type ToastListener = (t: ToastEvent) => void;
const toastListeners = new Set<ToastListener>();
export function onToast(fn: ToastListener): () => void {
  toastListeners.add(fn);
  return () => toastListeners.delete(fn);
}
export function toast(text: string, isErr = false): void {
  for (const fn of toastListeners) fn({ text, isErr });
}

export { onOpenChannelRequest } from "./notify";

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [phase, setPhase] = useState<Phase>("boot");
  const [serverUrl, setServerUrlState] = useState(DEFAULT_SERVER);

  const apiRef = useRef(new ApiClient({ baseUrl: DEFAULT_SERVER }));
  const logoutRef = useRef<() => Promise<void>>(async () => {});
  const storeRef = useRef<ChatStore | null>(null);
  if (!storeRef.current) {
    storeRef.current = new ChatStore({
      api: apiRef.current,
      wsUrl: () => wsUrlFor(apiRef.current.baseUrl, apiRef.current.token),
      get token() {
        return apiRef.current.token;
      },
      storage: prefStorage,
      onToast: (t) => toast(t.text, t.isErr),
      onNotify: (n: NotifyEvent) => {
        void presentMessageNotification(n);
      },
      onUnauthorized: () => {
        void logoutRef.current();
      },
    });
  }
  const store = storeRef.current;
  const api = apiRef.current;

  logoutRef.current = async () => {
    try {
      await api.logout();
    } catch {
      /* best effort */
    }
    store.reset();
    api.setToken(null);
    await kv.del(TOKEN_KEY);
    setPhase("login");
  };

  // Boot: hydrate prefs + persisted server/token, then either connect or
  // fall through to the login screen.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      await hydratePrefs();
      // iOS Keychain items survive uninstall; AsyncStorage does not. A missing
      // sentinel means a fresh install — drop any inherited secrets.
      if ((await AsyncStorage.getItem("installed")) == null) {
        await Promise.all([kv.del(TOKEN_KEY), kv.del(SERVER_KEY)]);
        await AsyncStorage.setItem("installed", "1");
      }
      const savedServer = await kv.get(SERVER_KEY);
      const base = normalizeServerUrl(savedServer || DEFAULT_SERVER);
      api.baseUrl = base;
      if (!cancelled) setServerUrlState(base);
      const token = await kv.get(TOKEN_KEY);
      if (!token) {
        if (!cancelled) setPhase("login");
        return;
      }
      api.setToken(token);
      try {
        await api.getMe();
      } catch {
        await kv.del(TOKEN_KEY);
        api.setToken(null);
        if (!cancelled) setPhase("login");
        return;
      }
      if (cancelled) return;
      store.connect();
      setPhase("ready");
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Foreground tracking — mirrors the web's visibilitychange wiring and the
  // legacy Android client's foreground flag: gates mark-read vs notify.
  useEffect(() => {
    const sub = AppState.addEventListener("change", (s) => {
      store.setForeground(s === "active");
    });
    store.setForeground(AppState.currentState === "active");
    return () => sub.remove();
  }, [store]);

  const login = useCallback(
    async (username: string, password: string): Promise<string | null> => {
      try {
        const res = await api.login(username.trim(), password);
        const token = res.session_token;
        if (!token) return "Login failed.";
        api.setToken(token);
        await kv.set(TOKEN_KEY, token);
        store.connect();
        setPhase("ready");
        return null;
      } catch (e) {
        const status = (e as { status?: number }).status;
        if (status === 403) return "Your account is pending approval by an admin.";
        if (status === 401) return "Invalid username or password.";
        return "Couldn't reach the server.";
      }
    },
    [api, store]
  );

  const register = useCallback(
    async (username: string, displayName: string, password: string) => {
      try {
        const res = await api.register(username.trim(), password, displayName.trim());
        if (res.status === "pending") return { ok: true as const };
        return { ok: true as const };
      } catch (e) {
        const err = e as { status?: number; detail?: string; message?: string };
        if (err.status === 409)
          return { ok: false as const, error: "That username is taken." };
        return { ok: false as const, error: err.detail || "Registration failed." };
      }
    },
    [api]
  );

  const setServerUrl = useCallback(
    async (url: string) => {
      const base = normalizeServerUrl(url);
      api.baseUrl = base;
      setServerUrlState(base);
      await kv.set(SERVER_KEY, base);
    },
    [api]
  );

  const value: Session = {
    phase,
    api,
    store,
    serverUrl,
    login,
    register,
    logout: () => logoutRef.current(),
    setServerUrl,
  };
  return <SessionCtx.Provider value={value}>{children}</SessionCtx.Provider>;
}

export function useSession(): Session {
  const s = useContext(SessionCtx);
  if (!s) throw new Error("useSession outside SessionProvider");
  return s;
}

export function useChatState(): ChatState {
  const { store } = useSession();
  useSyncExternalStore(
    (cb) => store.subscribe(cb),
    () => store.version,
    () => store.version
  );
  return store.state;
}
