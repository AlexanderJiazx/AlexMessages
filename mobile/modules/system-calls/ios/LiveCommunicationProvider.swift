import AVFAudio
import LiveCommunicationKit

/// LiveCommunicationKit (iOS 17.4+): the system call UI in mainland China,
/// where apps may not use CallKit. The same calls as CallKitProvider.
@available(iOS 17.4, *)
final class LiveCommunicationProvider: SystemCallProvider, ConversationManagerDelegate {
  weak var delegate: SystemCallProviderDelegate?
  private let manager: ConversationManager
  /// Actions this app requested itself: not echoed to the delegate.
  private var requested = Set<UUID>()

  init(icon: Data?, ringtone: String?) {
    let config = ConversationManager.Configuration(
      ringtoneName: ringtone,
      iconTemplateImageData: icon,
      maximumConversationGroups: 1,
      maximumConversationsPerConversationGroup: 1,
      includesConversationInRecents: false,
      supportsVideo: true,
      supportedHandleTypes: [.generic]
    )
    manager = ConversationManager(configuration: config)
    manager.delegate = self
  }

  func reportIncoming(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    let peer = Self.handle(for: call)
    let update = Conversation.Update(
      members: [peer],
      activeRemoteMembers: [peer],
      capabilities: call.video ? [.video] : []
    )
    let manager = self.manager
    Task { @MainActor in
      do {
        try await manager.reportNewIncomingConversation(uuid: call.uuid, update: update)
        completion(nil)
      } catch {
        completion(Self.failure(error))
      }
    }
  }

  func startOutgoing(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    perform(StartConversationAction(conversationUUID: call.uuid, handles: [Self.handle(for: call)], isVideo: call.video), completion)
  }

  func reportConnecting(_ call: SystemCall) {
    report(.conversationStartedConnecting(Date()), for: call)
  }

  func reportConnected(_ call: SystemCall) {
    report(.conversationConnected(Date()), for: call)
  }

  func reportEnded(_ call: SystemCall, reason: SystemCallEnd) {
    report(.conversationEnded(Date(), Self.reason(reason)), for: call)
  }

  func requestAnswer(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    perform(JoinConversationAction(conversationUUID: call.uuid), completion)
  }

  func requestEnd(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void) {
    perform(EndConversationAction(conversationUUID: call.uuid), completion)
  }

  func requestMute(_ call: SystemCall, muted: Bool, completion: @escaping (SystemCallFailure?) -> Void) {
    perform(MuteConversationAction(conversationUUID: call.uuid, isMuted: muted), completion)
  }

  private func perform(_ action: ConversationAction, _ completion: @escaping (SystemCallFailure?) -> Void) {
    requested.insert(action.uuid)
    let manager = self.manager
    Task { @MainActor in
      do {
        try await manager.perform([action])
        completion(nil)
      } catch {
        self.requested.remove(action.uuid)
        completion(Self.failure(error))
      }
    }
  }

  private func report(_ event: Conversation.Event, for call: SystemCall) {
    guard let conversation = manager.conversations.first(where: { $0.uuid == call.uuid }) else { return }
    manager.reportConversationEvent(event, for: conversation)
  }

  // MARK: ConversationManagerDelegate (any queue: hop to main)

  func conversationManager(_ manager: ConversationManager, conversationChanged conversation: Conversation) {}

  func conversationManagerDidBegin(_ manager: ConversationManager) {}

  func conversationManagerDidReset(_ manager: ConversationManager) {
    DispatchQueue.main.async {
      self.requested.removeAll()
      self.delegate?.providerDidReset()
    }
  }

  func conversationManager(_ manager: ConversationManager, perform action: ConversationAction) {
    DispatchQueue.main.async { self.handle(action) }
  }

  func conversationManager(_ manager: ConversationManager, timedOutPerforming action: ConversationAction) {}

  func conversationManager(_ manager: ConversationManager, didActivate audioSession: AVAudioSession) {
    DispatchQueue.main.async { self.delegate?.providerDidActivate(audioSession) }
  }

  func conversationManager(_ manager: ConversationManager, didDeactivate audioSession: AVAudioSession) {
    DispatchQueue.main.async { self.delegate?.providerDidDeactivate(audioSession) }
  }

  private func handle(_ action: ConversationAction) {
    let fromSystem = requested.remove(action.uuid) == nil
    let uuid = action.conversationUUID
    switch action {
    case let start as StartConversationAction:
      delegate?.providerWillConnect(uuid)
      start.fulfill(dateStarted: Date())
    case let join as JoinConversationAction:
      delegate?.providerWillConnect(uuid)
      join.fulfill(dateConnected: Date())
      if fromSystem { delegate?.providerDidAnswer(uuid) }
    case let end as EndConversationAction:
      end.fulfill(dateEnded: Date())
      if fromSystem { delegate?.providerDidEnd(uuid) }
    case let mute as MuteConversationAction:
      mute.fulfill()
      if fromSystem { delegate?.providerDidMute(uuid, muted: mute.isMuted) }
    default:
      // Pausing, merging, tones: one call at a time, no hold.
      action.fail()
    }
  }

  // MARK: Helpers

  private static func handle(for call: SystemCall) -> Handle {
    Handle(type: .generic, value: call.handle, displayName: call.name)
  }

  private static func failure(_ error: Error) -> SystemCallFailure {
    let ns = error as NSError
    return .refused("\(ns.domain) \(ns.code): \(ns.localizedDescription)")
  }

  private static func reason(_ end: SystemCallEnd) -> Conversation.EndedReason {
    switch end {
    case .failed: return .failed
    case .remoteEnded: return .remoteEnded
    case .unanswered: return .unanswered
    case .answeredElsewhere: return .joinedElsewhere
    case .declinedElsewhere: return .declinedElsewhere
    }
  }
}
