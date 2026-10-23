import AVFAudio
import CallKit

/// CallKit: the full-screen incoming call on the lock screen, the banner
/// while the phone is in use, the call in the status bar, and the audio
/// session the system activates for the call.
final class CallKitProvider: NSObject, SystemCallProvider, CXProviderDelegate {
  weak var delegate: SystemCallProviderDelegate?
  private let provider: CXProvider
  private let controller = CXCallController()
  /// Actions this app requested itself: not echoed to the delegate.
  private var requested = Set<UUID>()

  init(icon: Data?, ringtone: String?) {
    let config = CXProviderConfiguration()
    config.supportsVideo = true
    config.maximumCallGroups = 1
    config.maximumCallsPerCallGroup = 1
    config.supportedHandleTypes = [.generic]
    // A Recents entry calls back through an INStartCallIntent, which the
    // app doesn't handle.
    config.includesCallsInRecents = false
    config.iconTemplateImageData = icon
    config.ringtoneSound = ringtone
    provider = CXProvider(configuration: config)
    super.init()
    provider.setDelegate(self, queue: nil)
  }

  func reportIncoming(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    provider.reportNewIncomingCall(with: call.uuid, update: Self.update(for: call)) { error in
      DispatchQueue.main.async { completion(Self.failure(error)) }
    }
  }

  func startOutgoing(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    let action = CXStartCallAction(call: call.uuid, handle: CXHandle(type: .generic, value: call.handle))
    action.isVideo = call.video
    request(action) { failure in
      // The start action carries no name: give the system UI the peer's.
      if failure == nil { self.provider.reportCall(with: call.uuid, updated: Self.update(for: call)) }
      completion(failure)
    }
  }

  func reportConnecting(_ call: SystemCall) {
    provider.reportOutgoingCall(with: call.uuid, startedConnectingAt: nil)
  }

  func reportConnected(_ call: SystemCall) {
    provider.reportOutgoingCall(with: call.uuid, connectedAt: nil)
  }

  func reportEnded(_ call: SystemCall, reason: SystemCallEnd) {
    provider.reportCall(with: call.uuid, endedAt: nil, reason: Self.reason(reason))
  }

  func requestAnswer(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    request(CXAnswerCallAction(call: call.uuid), completion)
  }

  func requestEnd(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    request(CXEndCallAction(call: call.uuid), completion)
  }

  func requestMute(_ call: SystemCall, muted: Bool, completion: @escaping (SystemCallFailure?) -> Void) {
    request(CXSetMutedCallAction(call: call.uuid, muted: muted), completion)
  }

  private func request(_ action: CXCallAction, _ completion: @escaping (SystemCallFailure?) -> Void) {
    requested.insert(action.uuid)
    controller.request(CXTransaction(action: action)) { error in
      DispatchQueue.main.async {
        if error != nil { self.requested.remove(action.uuid) }
        completion(Self.failure(error))
      }
    }
  }

  // MARK: CXProviderDelegate

  func providerDidReset(_ provider: CXProvider) {
    requested.removeAll()
    delegate?.providerDidReset()
  }

  func provider(_ provider: CXProvider, perform action: CXStartCallAction) {
    requested.remove(action.uuid)
    delegate?.providerWillConnect(action.callUUID)
    action.fulfill()
  }

  func provider(_ provider: CXProvider, perform action: CXAnswerCallAction) {
    let fromSystem = requested.remove(action.uuid) == nil
    delegate?.providerWillConnect(action.callUUID)
    action.fulfill()
    if fromSystem { delegate?.providerDidAnswer(action.callUUID) }
  }

  func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
    let fromSystem = requested.remove(action.uuid) == nil
    action.fulfill()
    if fromSystem { delegate?.providerDidEnd(action.callUUID) }
  }

  func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
    let fromSystem = requested.remove(action.uuid) == nil
    action.fulfill()
    if fromSystem { delegate?.providerDidMute(action.callUUID, muted: action.isMuted) }
  }

  func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {
    delegate?.providerDidActivate(audioSession)
  }

  func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {
    delegate?.providerDidDeactivate(audioSession)
  }

  // MARK: Helpers

  private static func update(for call: SystemCall) -> CXCallUpdate {
    let update = CXCallUpdate()
    update.remoteHandle = CXHandle(type: .generic, value: call.handle)
    update.localizedCallerName = call.name
    update.hasVideo = call.video
    update.supportsHolding = false
    update.supportsGrouping = false
    update.supportsUngrouping = false
    update.supportsDTMF = false
    return update
  }

  private static func failure(_ error: Error?) -> SystemCallFailure? {
    guard let error else { return nil }
    if let incoming = error as? CXErrorCodeIncomingCallError,
       incoming.code == .filteredByDoNotDisturb || incoming.code == .filteredByBlockList {
      return .filtered
    }
    let ns = error as NSError
    return .refused("\(ns.domain) \(ns.code): \(ns.localizedDescription)")
  }

  private static func reason(_ end: SystemCallEnd) -> CXCallEndedReason {
    switch end {
    case .failed: return .failed
    case .remoteEnded: return .remoteEnded
    case .unanswered: return .unanswered
    case .answeredElsewhere: return .answeredElsewhere
    case .declinedElsewhere: return .declinedElsewhere
    }
  }
}
