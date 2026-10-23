import AVFAudio
import ExpoModulesCore

/// The iOS system call UI for DM calls (src/call/system.ios.ts drives it):
/// CallKit, or LiveCommunicationKit in mainland China, where apps may not
/// use CallKit (MIIT, 2018) and LiveCommunicationKit (iOS 17.4) takes its
/// place. Older iOS in China and the iOS Simulator get neither (`kind`
/// "none"), and the app rings and plays its call audio itself, as it did
/// before.
///
/// JS reports each call by its CallSession key. What the user does in the
/// system UI (answer, end, mute) comes back as events. The system owns the
/// call's audio session: see CallAudio.
///
/// Only calls that arrive while the app runs can ring: without VoIP push
/// (PushKit, which needs a paid developer account), a suspended app hears
/// no call.
public final class SystemCallsModule: Module {
  public func definition() -> ModuleDefinition {
    Name("SystemCalls")

    Events("onAnswer", "onEnd", "onMute", "onReset", "onSpeaker")

    /// "callkit", "livecommunication", or "none".
    Constant("kind") { SystemCalls.shared.kind }

    // Expo runs these on the JS thread, and SystemCalls is main-queue only.
    OnCreate {
      DispatchQueue.main.async { SystemCalls.shared.attach(self) }
    }

    OnDestroy {
      DispatchQueue.main.async { SystemCalls.shared.detach(self) }
    }

    /// Rejects with ERR_FILTERED when Do Not Disturb, a Focus, or the block
    /// list silenced the call, and ERR_REFUSED for anything else: the app
    /// then rings (or stays silent) by itself.
    AsyncFunction("reportIncoming") { (key: Int, handle: String, name: String, video: Bool, promise: Promise) in
      SystemCalls.shared.reportIncoming(key: key, handle: handle, name: name, video: video, promise: promise)
    }
    .runOnQueue(.main)

    AsyncFunction("startOutgoing") { (key: Int, handle: String, name: String, video: Bool, promise: Promise) in
      SystemCalls.shared.startOutgoing(key: key, handle: handle, name: name, video: video, promise: promise)
    }
    .runOnQueue(.main)

    AsyncFunction("reportConnecting") { (key: Int) in
      SystemCalls.shared.reportConnecting(key: key)
    }
    .runOnQueue(.main)

    AsyncFunction("reportConnected") { (key: Int) in
      SystemCalls.shared.reportConnected(key: key)
    }
    .runOnQueue(.main)

    /// `reason`: failed, remoteEnded, unanswered, answeredElsewhere, or
    /// declinedElsewhere.
    AsyncFunction("reportEnded") { (key: Int, reason: String) in
      SystemCalls.shared.reportEnded(key: key, reason: SystemCallEnd(rawValue: reason) ?? .failed)
    }
    .runOnQueue(.main)

    AsyncFunction("requestAnswer") { (key: Int, promise: Promise) in
      SystemCalls.shared.requestAnswer(key: key, promise: promise)
    }
    .runOnQueue(.main)

    AsyncFunction("requestEnd") { (key: Int, promise: Promise) in
      SystemCalls.shared.requestEnd(key: key, promise: promise)
    }
    .runOnQueue(.main)

    AsyncFunction("requestMute") { (key: Int, muted: Bool, promise: Promise) in
      SystemCalls.shared.requestMute(key: key, muted: muted, promise: promise)
    }
    .runOnQueue(.main)

    AsyncFunction("startRingback") {
      SystemCalls.shared.audio.startRingback()
    }
    .runOnQueue(.main)

    AsyncFunction("stopRingback") {
      SystemCalls.shared.audio.stopRingback()
    }
    .runOnQueue(.main)

    AsyncFunction("setSpeaker") { (on: Bool) in
      SystemCalls.shared.audio.setSpeaker(on)
    }
    .runOnQueue(.main)

    AsyncFunction("setInCall") { (on: Bool) in
      SystemCalls.shared.audio.setInCall(on)
    }
    .runOnQueue(.main)
  }

  func emit(_ event: String, _ body: [String: Any?] = [:]) {
    sendEvent(event, body)
  }
}

/// One per process: the system keeps one provider per app, and its calls
/// outlive a JS reload. Main queue only.
final class SystemCalls: SystemCallProviderDelegate {
  static let shared = SystemCalls()

  let kind: String
  let audio = CallAudio()
  private lazy var provider: SystemCallProvider? = makeProvider()
  private var calls: [Int: SystemCall] = [:]
  private weak var module: SystemCallsModule?

  private init() {
#if targetEnvironment(simulator)
    // The Simulator has no system call UI: iOS ends every call reported to
    // CallKit or LiveCommunicationKit there within seconds ("there wont be a
    // UI to host the call"), as if the user had hung up.
    kind = "none"
#else
    if Locale.current.region?.identifier != "CN" {
      kind = "callkit"
    } else if #available(iOS 17.4, *) {
      kind = "livecommunication"
    } else {
      kind = "none"
    }
#endif
    audio.onSpeakerChange = { [weak self] on in self?.module?.emit("onSpeaker", ["on": on]) }
  }

  func attach(_ module: SystemCallsModule) {
    self.module = module
    // A JS reload forgot the calls it reported: end them in the system too.
    let orphans = Array(calls.values)
    calls.removeAll()
    for call in orphans { provider?.reportEnded(call, reason: .failed) }
    if !orphans.isEmpty { audio.reset() }
  }

  func detach(_ module: SystemCallsModule) {
    if self.module === module { self.module = nil }
  }

  func reportIncoming(key: Int, handle: String, name: String, video: Bool, promise: Promise) {
    guard let provider else {
      promise.reject("ERR_UNSUPPORTED", "No system call UI on this device")
      return
    }
    let call = SystemCall(key: key, uuid: UUID(), handle: handle, name: name, video: video)
    calls[key] = call
    audio.begin(video: video)
    provider.reportIncoming(call) { failure in
      self.settle(call, failure, promise)
    }
  }

  func startOutgoing(key: Int, handle: String, name: String, video: Bool, promise: Promise) {
    guard let provider else {
      promise.reject("ERR_UNSUPPORTED", "No system call UI on this device")
      return
    }
    let call = SystemCall(key: key, uuid: UUID(), handle: handle, name: name, video: video)
    calls[key] = call
    audio.begin(video: video)
    provider.startOutgoing(call) { failure in
      self.settle(call, failure, promise)
    }
  }

  /// A refused call is forgotten here, and its audio goes back to WebRTC.
  private func settle(_ call: SystemCall, _ failure: SystemCallFailure?, _ promise: Promise) {
    guard let failure else {
      promise.resolve(nil)
      return
    }
    if calls[call.key]?.uuid == call.uuid {
      calls[call.key] = nil
      audio.release()
    }
    promise.reject(failure.code, failure.message)
  }

  func reportConnecting(key: Int) {
    if let call = calls[key] { provider?.reportConnecting(call) }
  }

  func reportConnected(key: Int) {
    if let call = calls[key] { provider?.reportConnected(call) }
  }

  func reportEnded(key: Int, reason: SystemCallEnd) {
    guard let call = calls.removeValue(forKey: key) else { return }
    provider?.reportEnded(call, reason: reason)
  }

  func requestAnswer(key: Int, promise: Promise) {
    guard let call = calls[key], let provider else {
      promise.reject("ERR_NO_CALL", "The system has no such call")
      return
    }
    provider.requestAnswer(call) { failure in
      if let failure {
        // The system lost the call: the app runs its audio from here on.
        if self.calls[key]?.uuid == call.uuid { self.calls[key] = nil }
        self.provider?.reportEnded(call, reason: .failed)
        self.audio.release()
        promise.reject(failure.code, failure.message)
      } else {
        promise.resolve(nil)
      }
    }
  }

  func requestEnd(key: Int, promise: Promise) {
    guard let call = calls.removeValue(forKey: key), let provider else {
      promise.resolve(nil)
      return
    }
    provider.requestEnd(call) { failure in
      // Refused (already over there?): make sure the system UI clears.
      if failure != nil { self.provider?.reportEnded(call, reason: .failed) }
      promise.resolve(nil)
    }
  }

  func requestMute(key: Int, muted: Bool, promise: Promise) {
    guard let call = calls[key], let provider else {
      promise.resolve(nil)
      return
    }
    provider.requestMute(call, muted: muted) { failure in
      if let failure {
        promise.reject(failure.code, failure.message)
      } else {
        promise.resolve(nil)
      }
    }
  }

  // MARK: SystemCallProviderDelegate

  func providerWillConnect(_ uuid: UUID) {
    if let call = call(uuid) { audio.configure(video: call.video) }
  }

  func providerDidAnswer(_ uuid: UUID) {
    if let call = call(uuid) { module?.emit("onAnswer", ["key": call.key]) }
  }

  func providerDidEnd(_ uuid: UUID) {
    guard let call = call(uuid) else { return }
    calls[call.key] = nil
    module?.emit("onEnd", ["key": call.key])
  }

  func providerDidMute(_ uuid: UUID, muted: Bool) {
    if let call = call(uuid) { module?.emit("onMute", ["key": call.key, "muted": muted]) }
  }

  func providerDidActivate(_ session: AVAudioSession) {
    audio.activated(session)
  }

  func providerDidDeactivate(_ session: AVAudioSession) {
    audio.deactivated(session)
  }

  func providerDidReset() {
    calls.removeAll()
    audio.reset()
    module?.emit("onReset")
  }

  // MARK: Helpers

  private func call(_ uuid: UUID) -> SystemCall? {
    calls.values.first { $0.uuid == uuid }
  }

  private func makeProvider() -> SystemCallProvider? {
    let icon = Self.iconData()
    let ringtone = Bundle.main.url(forResource: "incallmanager_ringtone", withExtension: "mp3") != nil
      ? "incallmanager_ringtone.mp3" : nil
    let provider: SystemCallProvider?
    switch kind {
    case "callkit":
      provider = CallKitProvider(icon: icon, ringtone: ringtone)
    case "livecommunication":
      if #available(iOS 17.4, *) {
        provider = LiveCommunicationProvider(icon: icon, ringtone: ringtone)
      } else {
        provider = nil
      }
    default:
      provider = nil
    }
    provider?.delegate = self
    return provider
  }

  /// The button in the system call UI that opens the app: the ring logo,
  /// 40 pt (Resources/CallIcon.png, from the Android monochrome icon).
  private static func iconData() -> Data? {
    let bundle = Bundle.main.url(forResource: "SystemCalls", withExtension: "bundle").flatMap(Bundle.init(url:))
    guard let url = bundle?.url(forResource: "CallIcon", withExtension: "png") else { return nil }
    return try? Data(contentsOf: url)
  }
}
