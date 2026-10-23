/**
 * Expo web: no calls (react-native-webrtc is native only). iOS and Android
 * load platform.native.ts instead. The web client in web/ has calls.
 */
import type { CallPlatform } from "@alexmessages/shared";

export const callPlatform: CallPlatform | null = null;
