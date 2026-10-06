import { useEffect, useState } from "react";
import { nameFor } from "@shared/format";
import { store } from "../client";
import { useChatState } from "../hooks";
import { Rail } from "../components/Rail";
import { Topbar } from "../components/Topbar";
import { Stream } from "../components/Stream";
import { Composer } from "../components/Composer";
import { SettingsModal } from "../components/SettingsModal";
import {
  NewDmModal,
  DeleteAccountModal,
  ProfileSheet,
  PushPromptModal,
} from "../components/Modals";
import { Toasts, Lightbox } from "../components/Overlays";
import { reconcilePushOnLoad, registerServiceWorker } from "../push";

/** The signed-in app shell: rail + center pane + every overlay. */
export function ChatScreen() {
  const s = useChatState();
  const [railOpen, setRailOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [newDmOpen, setNewDmOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [pushPrompt, setPushPrompt] = useState(false);
  const [profileUid, setProfileUid] = useState<number | null>(null);
  const [lightboxSrc, setLightboxSrc] = useState<string | null>(null);

  // Eager SW registration + push reconciliation after the first init frame.
  useEffect(() => {
    registerServiceWorker();
  }, []);

  const ready = s.ready;
  useEffect(() => {
    if (!ready) return;
    void reconcilePushOnLoad().then((r) => {
      if (r === "prompt") setPushPrompt(true);
    });
  }, [ready]);

  const peerId = store.peerOf(s.activeChannel);
  const peerName = nameFor(s.users, peerId);

  return (
    <>
      <div className="app">
        <div
          className={`backdrop${railOpen ? " on" : ""}`}
          onClick={() => setRailOpen(false)}
        />
        <Rail
          open={railOpen}
          onClose={() => setRailOpen(false)}
          onNewDm={() => setNewDmOpen(true)}
          onOpenSettings={() => {
            setSettingsOpen(true);
            setRailOpen(false);
          }}
        />
        <main className="pane center">
          <Topbar onMenu={() => setRailOpen(true)} />
          <Stream
            onOpenProfile={(uid) => {
              if (s.me && uid === s.me.id) setSettingsOpen(true);
              else setProfileUid(uid);
            }}
            onImageClick={setLightboxSrc}
          />
          <Composer peerName={peerName} />
        </main>
      </div>

      <Toasts />
      <Lightbox src={lightboxSrc} onClose={() => setLightboxSrc(null)} />

      <SettingsModal
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        onOpenDelete={() => setDeleteOpen(true)}
      />
      <DeleteAccountModal on={deleteOpen} onClose={() => setDeleteOpen(false)} />
      <ProfileSheet uid={profileUid} onClose={() => setProfileUid(null)} />
      <NewDmModal on={newDmOpen} onClose={() => setNewDmOpen(false)} />
      <PushPromptModal on={pushPrompt} onClose={() => setPushPrompt(false)} />
    </>
  );
}
