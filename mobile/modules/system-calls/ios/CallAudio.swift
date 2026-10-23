import AVFAudio
import UIKit
import WebRTC

/// The audio of a call the system call UI runs. The system activates and
/// deactivates the app's audio session (the CallKit rule); WebRTC waits for
/// that (manual audio) instead of activating the session itself, and the
/// ringback plays only inside it. A call without the system UI leaves all of
/// this to react-native-incall-manager in JS (src/call/callAudio.native.ts),
/// which this mirrors: the ringback file, the earpiece/loudspeaker switch,
/// the proximity sensor for voice calls, and the screen kept on.
final class CallAudio {
  /// The output route changed outside the app (the system call UI's audio
  /// button, a headset): true when it is the loudspeaker now.
  var onSpeakerChange: ((Bool) -> Void)?

  private let rtc = RTCAudioSession.sharedInstance()
  private var video = false
  private var speaker = false
  private var inCall = false
  private var wantsRingback = false
  private var ringback: AVAudioPlayer?
  /// The system activated the session for the current call.
  private var active = false
  private var routeObserver: NSObjectProtocol?

  init() {
    routeObserver = NotificationCenter.default.addObserver(
      forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main
    ) { [weak self] _ in self?.routeChanged() }
  }

  deinit {
    if let routeObserver { NotificationCenter.default.removeObserver(routeObserver) }
  }

  /// A system call starts: WebRTC may not start its audio unit until the
  /// system activates the session. Video calls start on the loudspeaker.
  func begin(video: Bool) {
    self.video = video
    speaker = video
    wantsRingback = false
    rtc.useManualAudio = true
    rtc.isAudioEnabled = active
  }

  /// The call gets no system UI after all: WebRTC (and InCallManager, from
  /// JS) activate the session themselves, as before CallKit.
  func release() {
    stopPlayer()
    wantsRingback = false
    rtc.useManualAudio = false
  }

  /// The call starts or is answered: the category and mode the system then
  /// activates. WebRTC applies its own configuration whenever it starts its
  /// audio unit, so that carries the same mode.
  func configure(video: Bool) {
    self.video = video
    let config = RTCAudioSessionConfiguration.webRTC()
    config.category = AVAudioSession.Category.playAndRecord.rawValue
    config.categoryOptions = [.allowBluetoothHFP]
    config.mode = mode.rawValue
    RTCAudioSessionConfiguration.setWebRTC(config)
    rtc.lockForConfiguration()
    defer { rtc.unlockForConfiguration() }
    do {
      try rtc.setConfiguration(config)
    } catch {
      NSLog("[SystemCalls] audio configuration failed: \(error)")
    }
  }

  func activated(_ session: AVAudioSession) {
    active = true
    rtc.audioSessionDidActivate(session)
    rtc.isAudioEnabled = true
    applyRoute()
    if wantsRingback { playRingback() }
  }

  func deactivated(_ session: AVAudioSession) {
    active = false
    stopPlayer()
    rtc.audioSessionDidDeactivate(session)
    rtc.isAudioEnabled = false
  }

  /// The ringback a caller hears while the peer's phone rings. It waits for
  /// the system to activate the session.
  func startRingback() {
    wantsRingback = true
    if active { playRingback() }
  }

  func stopRingback() {
    wantsRingback = false
    stopPlayer()
  }

  /// true: the loudspeaker; false: the earpiece (a headset when one is
  /// connected), also for a video call.
  func setSpeaker(_ on: Bool) {
    speaker = on
    applyRoute()
  }

  /// From dialing or answering until the call ends: the screen stays on,
  /// and a voice call turns it off at the ear.
  func setInCall(_ on: Bool) {
    inCall = on
    UIApplication.shared.isIdleTimerDisabled = on
    UIDevice.current.isProximityMonitoringEnabled = on && !video
  }

  /// Every call is over (a reset, or a JS reload).
  func reset() {
    release()
    setInCall(false)
  }

  /// videoChat routes to the loudspeaker; a video call leaves it through
  /// voiceChat, as InCallManager does.
  private var mode: AVAudioSession.Mode {
    video && speaker ? .videoChat : .voiceChat
  }

  private func applyRoute() {
    guard active else { return }
    let config = RTCAudioSessionConfiguration.webRTC()
    config.mode = mode.rawValue
    RTCAudioSessionConfiguration.setWebRTC(config)
    rtc.lockForConfiguration()
    defer { rtc.unlockForConfiguration() }
    do {
      if rtc.mode != mode.rawValue { try rtc.setMode(mode) }
      try rtc.overrideOutputAudioPort(speaker ? .speaker : .none)
    } catch {
      NSLog("[SystemCalls] audio route failed: \(error)")
    }
  }

  private func routeChanged() {
    guard active else { return }
    let onSpeaker = rtc.session.currentRoute.outputs.contains { $0.portType == .builtInSpeaker }
    guard onSpeaker != speaker else { return }
    speaker = onSpeaker
    onSpeakerChange?(onSpeaker)
  }

  private func playRingback() {
    guard ringback == nil,
          let url = Bundle.main.url(forResource: "incallmanager_ringback", withExtension: "mp3") else { return }
    do {
      let player = try AVAudioPlayer(contentsOf: url)
      player.numberOfLoops = -1
      player.play()
      ringback = player
    } catch {
      NSLog("[SystemCalls] ringback failed: \(error)")
    }
  }

  private func stopPlayer() {
    ringback?.stop()
    ringback = nil
  }
}
