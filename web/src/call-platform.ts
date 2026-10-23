/**
 * The browser's WebRTC behind the shared CallManager (see shared/src/call.ts).
 * Null where calls can't work: a browser without WebRTC, or a page served
 * over plain http from anywhere but localhost (no getUserMedia there).
 */
import type {
  CallFacing,
  CallMediaConstraints,
  CallPeerConnection,
  CallPlatform,
  CallStream,
} from "@shared/call";

/** What a call asks every camera for. */
const CAMERA_QUALITY = { width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 30 } };

/** The camera this page opened last: its deviceId, the facing its request
 *  asked for, and whether the camera reported a facing mode of its own. */
let lastCamera: { deviceId: string; facing: CallFacing; reportsFacing: boolean } | null = null;

async function getUserMedia(c: CallMediaConstraints): Promise<CallStream> {
  // A call's first capture asks for the microphone as well and opens the
  // default camera. Later requests for the camera alone (camera back on,
  // Switch camera) start from the camera opened last.
  if (c.audio) lastCamera = null;
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: c.audio ? { echoCancellation: true, noiseSuppression: true, autoGainControl: true } : false,
    video: c.video ? { ...(await pickCamera(c.video.facingMode)), ...CAMERA_QUALITY } : false,
  });
  const settings = stream.getVideoTracks()[0]?.getSettings();
  if (c.video && settings) {
    lastCamera = { deviceId: settings.deviceId ?? "", facing: c.video.facingMode, reportsFacing: !!settings.facingMode };
  }
  return stream as unknown as CallStream;
}

/**
 * The constraint that opens the camera for `facing`. Phones report which
 * way each camera faces, so asking for the facing picks the camera. Desktop
 * webcams report no facing mode, and the browser would open the default
 * camera for either facing; there a new facing (Switch camera) opens the
 * next camera by deviceId, and the same facing (camera back on) reopens the
 * camera opened last.
 */
async function pickCamera(facing: CallFacing): Promise<MediaTrackConstraints> {
  const last = lastCamera;
  if (!last) return { facingMode: facing };
  const cameras = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === "videoinput");
  if (facingSwitches(cameras, last.reportsFacing)) return { facingMode: facing };
  const at = cameras.findIndex((d) => d.deviceId === last.deviceId);
  if (at < 0) return { facingMode: facing }; // unplugged: the default camera
  const next = facing === last.facing ? cameras[at] : cameras[(at + 1) % cameras.length];
  return { deviceId: { exact: next.deviceId } };
}

/** Whether asking for a facing switches cameras: one camera faces the user
 *  and another faces away. Firefox has no InputDeviceInfo.getCapabilities(),
 *  so there the camera opened last decides by reporting a facing mode. */
function facingSwitches(cameras: MediaDeviceInfo[], lastReportsFacing: boolean): boolean {
  const modes = new Set<string>();
  for (const camera of cameras) {
    const caps = (camera as InputDeviceInfo).getCapabilities?.();
    if (!caps) return lastReportsFacing;
    for (const mode of caps.facingMode ?? []) modes.add(mode);
  }
  return modes.has("user") && modes.has("environment");
}

export const webCallPlatform: CallPlatform | null =
  typeof RTCPeerConnection !== "undefined" && typeof navigator !== "undefined" && !!navigator.mediaDevices?.getUserMedia
    ? {
        createPeerConnection: ({ iceServers }) => new RTCPeerConnection({ iceServers }) as unknown as CallPeerConnection,
        getUserMedia,
        createStream: (tracks) => new MediaStream(tracks as unknown as MediaStreamTrack[]) as unknown as CallStream,
      }
    : null;

/** Whether this device has more than one camera (the switch button). */
export async function hasSeveralCameras(): Promise<boolean> {
  try {
    const devices = await navigator.mediaDevices.enumerateDevices();
    return devices.filter((d) => d.kind === "videoinput").length > 1;
  } catch {
    return false;
  }
}

/** Whether the self view shows as a mirror, the way people expect to see
 *  themselves: unless its camera reports facing away (a phone's back
 *  camera). Goes by the camera that opened, not the facing asked for. */
export function mirrorsSelfView(stream: CallStream | null): boolean {
  const track = (stream as unknown as MediaStream | null)?.getVideoTracks()[0];
  return track?.getSettings().facingMode !== "environment";
}
