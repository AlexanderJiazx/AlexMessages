import AVFAudio
import Foundation

/// One DM call reported to the system call UI. `key` is the JS CallSession
/// key; the system knows the call by `uuid`.
struct SystemCall {
  let key: Int
  let uuid: UUID
  /// The peer's user id: the call's handle.
  let handle: String
  let name: String
  let video: Bool
}

/// Why a call ended, in the words both system frameworks use.
enum SystemCallEnd: String {
  case failed
  case remoteEnded
  case unanswered
  case answeredElsewhere
  case declinedElsewhere
}

enum SystemCallFailure: Error {
  /// Do Not Disturb, a Focus, or the block list silenced an incoming call.
  case filtered
  case refused(String)

  var code: String {
    switch self {
    case .filtered: return "ERR_FILTERED"
    case .refused: return "ERR_REFUSED"
    }
  }

  var message: String {
    switch self {
    case .filtered: return "The system silenced the call"
    case .refused(let reason): return reason
    }
  }
}

/// CallKit, or LiveCommunicationKit: the same calls through either.
protocol SystemCallProvider: AnyObject {
  var delegate: SystemCallProviderDelegate? { get set }

  /// Rings: the system's incoming-call UI and ringtone.
  func reportIncoming(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void)
  /// Asks the system to start an outgoing call.
  func startOutgoing(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void)
  /// Outgoing calls only: the peer answered, and media is connecting.
  func reportConnecting(_ call: SystemCall)
  /// Outgoing calls only: media flows (the system's call timer starts).
  func reportConnected(_ call: SystemCall)
  /// The call ended outside this device's system UI.
  func reportEnded(_ call: SystemCall, reason: SystemCallEnd)
  /// The user answered, hung up, or (un)muted in the app's own UI.
  func requestAnswer(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void)
  func requestEnd(_ call: SystemCall, completion: @escaping (SystemCallFailure?) -> Void)
  func requestMute(_ call: SystemCall, muted: Bool, completion: @escaping (SystemCallFailure?) -> Void)
}

/// What the system call UI tells the app. Every method runs on the main
/// queue. The answer, end, and mute callbacks come only from the system UI;
/// actions the app requested itself are not echoed back.
protocol SystemCallProviderDelegate: AnyObject {
  /// The call starts or is answered: set the audio session up before the
  /// system activates it.
  func providerWillConnect(_ uuid: UUID)
  func providerDidAnswer(_ uuid: UUID)
  func providerDidEnd(_ uuid: UUID)
  func providerDidMute(_ uuid: UUID, muted: Bool)
  func providerDidActivate(_ session: AVAudioSession)
  func providerDidDeactivate(_ session: AVAudioSession)
  /// The system dropped every call (e.g. its call service restarted).
  func providerDidReset()
}
