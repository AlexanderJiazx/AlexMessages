package expo.modules.callringer

import android.app.Activity
import android.app.KeyguardManager
import android.content.Intent
import android.os.Build
import android.os.SystemClock
import android.view.WindowManager

/**
 * Taps on the ring service's notifications, read from the activity intent
 * (CallIntentListener) and handed to the JS: "answer" and "open" (a ringing
 * call: its buttons, its body, or its full-screen launch), "chat" (a missed
 * call) and "app" (the "Ready for calls" notification). While the JS listens
 * they go out as onCallAction events; until then the newest one waits for
 * takeLaunchAction().
 */
internal object CallIntents {
  data class Action(val action: String, val callId: String?, val channel: String?, val at: Long) {
    fun toMap(): Map<String, Any?> = mapOf("action" to action, "callId" to callId, "channel" to channel)
  }

  // A call rings for 45 s; an older tap is no longer about anything live.
  private const val PENDING_MS = 60_000L

  /** Set by CallRingerModule while the JS listens for onCallAction. */
  @Volatile
  var listener: ((Action) -> Unit)? = null

  private var pending: Action? = null

  @Synchronized
  fun take(): Action? {
    val action = pending
    pending = null
    return action?.takeIf { SystemClock.elapsedRealtime() - it.at < PENDING_MS }
  }

  /** Main thread. fresh is false for an activity recreated from saved state. */
  fun handle(activity: Activity, intent: Intent?, fresh: Boolean): Boolean {
    if (intent == null || intent.action != RingNotifications.ACTION_CALL) return false
    if (!fresh || (intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) != 0) return false
    val action = intent.getStringExtra(RingNotifications.EXTRA_ACTION) ?: return false
    val callId = intent.getStringExtra(RingNotifications.EXTRA_CALL_ID)
    val channel = intent.getStringExtra(RingNotifications.EXTRA_CHANNEL)
    // Read once: the activity keeps this intent.
    intent.removeExtra(RingNotifications.EXTRA_ACTION)
    if (callId != null && (action == "answer" || action == "open")) {
      // The call shows over the lock screen. The JS turns this off once no
      // call is live (CallRinger.setShowWhenLocked).
      LockScreen.set(activity, true)
      if (action == "answer") RingService.instance?.answering(callId)
    }
    val tap = Action(action, callId, channel, SystemClock.elapsedRealtime())
    val deliver = listener
    if (deliver != null) {
      deliver(tap)
    } else {
      synchronized(this) { pending = tap }
    }
    return true
  }
}

/** Showing the app over the lock screen while a call is live. Main thread. */
internal object LockScreen {
  fun set(activity: Activity, on: Boolean) {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
      activity.setShowWhenLocked(on)
      activity.setTurnScreenOn(on)
    } else {
      @Suppress("DEPRECATION")
      val flags = WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON
      if (on) activity.window.addFlags(flags) else activity.window.clearFlags(flags)
    }
  }

  fun locked(activity: Activity): Boolean =
    (activity.getSystemService(Activity.KEYGUARD_SERVICE) as KeyguardManager).isKeyguardLocked

  /** Asks for the PIN (or fingerprint) before the app may leave the call screen. */
  fun requestUnlock(activity: Activity, done: (Boolean) -> Unit) {
    val keyguard = activity.getSystemService(Activity.KEYGUARD_SERVICE) as KeyguardManager
    if (!keyguard.isKeyguardLocked) {
      done(true)
      return
    }
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
      done(false)
      return
    }
    keyguard.requestDismissKeyguard(
      activity,
      object : KeyguardManager.KeyguardDismissCallback() {
        override fun onDismissSucceeded() = done(true)

        override fun onDismissCancelled() = done(false)

        override fun onDismissError() = done(false)
      }
    )
  }
}
