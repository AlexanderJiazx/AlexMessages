/**
 * @alexmessages/shared — framework-agnostic core for Alex Messages clients.
 *
 * Consumed by the React web app (web/) and the React Native app (mobile/) so
 * both platforms share wire types, REST/WS plumbing, the chat state machine,
 * and display formatting.
 */

export * from "./types";
export * from "./dm";
export * from "./format";
export * from "./api";
export * from "./socket";
export * from "./store";
export * from "./debug";
export * from "./wav";
