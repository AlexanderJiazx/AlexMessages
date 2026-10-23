package expo.modules.callringer

import android.app.AlarmManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.os.SystemClock
import android.util.Log
import androidx.core.content.ContextCompat
import org.json.JSONObject

/**
 * Lets calls ring while the app is closed on phones without Google Play
 * services (no FCM push to wake the app): a foreground service, with its own
 * minimized "Ready for calls" notification, that keeps the call listener
 * socket open (RingSocket, /ws?mode=calls) and rings with a CallStyle
 * notification (RingNotifications).
 *
 * While the app is on screen the JS rings instead. It claims each call it
 * rings, and this service holds its notification back until the JS releases
 * the call or the app leaves the screen. A call whose screen went off while
 * the app showed it (the power button: the app keeps the screen on while a
 * call rings) is silenced: its notification comes back without sound, and
 * claim tells the JS not to ring again.
 *
 * Main thread only.
 */
class RingService : Service(), RingSocket.Listener {
  companion object {
    private const val TAG = "CallRinger"
    private const val WATCHDOG_MS = 5 * 60_000L

    // The server pings every 30 s; this much silence means a dead link.
    private const val QUIET_MS = 150_000L
    private const val AWAKE_MS = 10_000L

    // The server rings a call for 45 s. A call still listed after this lost
    // its call_ended to a dropped socket.
    private const val STALE_MS = 60_000L

    /** The running service, from onCreate to onDestroy. */
    @Volatile
    internal var instance: RingService? = null
      private set

    private val main = Handler(Looper.getMainLooper())

    /** Runs block on the main thread with the running service, if any. */
    internal fun onMain(block: (RingService) -> Unit) {
      main.post { instance?.let(block) }
    }

    /** Saves the session and starts (or updates) the service. The app calls it in the foreground. */
    internal fun start(context: Context, config: RingStore.Config) {
      RingStore.save(context, config)
      ContextCompat.startForegroundService(context, Intent(context, RingService::class.java))
    }

    /**
     * Forgets the session and stops the service. Never stopService: stopping
     * a service that was started with startForegroundService before it
     * reached startForeground crashes the app. A start still on its way
     * finds no session and stops itself.
     */
    internal fun stop(context: Context) {
      RingStore.clear(context)
      instance?.shutdown()
    }

    /** Restarts a service the system stopped (boot, app update, watchdog). */
    internal fun revive(context: Context) {
      if (instance != null || RingStore.load(context) == null) return
      try {
        ContextCompat.startForegroundService(context, Intent(context, RingService::class.java))
      } catch (e: RuntimeException) {
        // Android 12+ refuses a foreground service start from the background
        // unless the app is exempt from battery optimization.
        Log.w(TAG, "Couldn't restart the ring service", e)
      }
    }
  }

  private var config: RingStore.Config? = null
  private var socket: RingSocket? = null
  private var foreground = false
  private var network: Network? = null
  private var networkCallback: ConnectivityManager.NetworkCallback? = null
  private lateinit var power: PowerManager
  private lateinit var wakeLock: PowerManager.WakeLock

  /** Calls ringing for this user, from call_incoming until call_ended. */
  private val ringing = LinkedHashMap<String, RingingCall>()

  /** When each ringing call was first heard of (elapsedRealtime). */
  private val since = HashMap<String, Long>()

  /** Calls the JS is ringing on screen. */
  private val claimed = HashSet<String>()

  /** Calls answered from the notification (the app is opening to take them). */
  private val answered = HashSet<String>()

  /** Calls declined from the notification, until the server ends them. */
  private val declined = HashSet<String>()

  private val silenced = HashSet<String>()

  /** Calls with a ringing notification up. */
  private val posted = HashSet<String>()

  override fun onCreate() {
    super.onCreate()
    instance = this
    power = getSystemService(POWER_SERVICE) as PowerManager
    wakeLock = power.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "AlexMessages:ring").apply {
      setReferenceCounted(false)
    }
    RingNotifications.createChannels(this)
    AppVisibility.onHidden = { refreshAll() }
    watchNetwork()
  }

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    // Every startForegroundService has to reach startForeground, even when
    // the service stops right after.
    try {
      val notification = RingNotifications.service(this)
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
        startForeground(RingNotifications.ID_SERVICE, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
      } else {
        startForeground(RingNotifications.ID_SERVICE, notification)
      }
      foreground = true
    } catch (e: RuntimeException) {
      // A sticky restart in the background may not go foreground (Android 12+).
      Log.w(TAG, "Couldn't start the ring service in the foreground", e)
      stopSelf()
      return START_NOT_STICKY
    }
    val next = RingStore.load(this)
    if (next == null) {
      stopSelf()
      return START_NOT_STICKY
    }
    if (!RingNotifications.allowed(this)) {
      // No call could show, so callers would ring for nothing. The app
      // starts the service again once notifications are allowed.
      stopSelf()
      return START_NOT_STICKY
    }
    if (next != config || socket == null) {
      config = next
      socket?.close()
      holdAwake()
      socket = RingSocket(listenerUrl(next), this).also { it.connect() }
    }
    armWatchdog()
    return START_STICKY
  }

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onDestroy() {
    instance = null
    AppVisibility.onHidden = null
    socket?.close()
    socket = null
    config = null
    networkCallback?.let { callback ->
      try {
        getSystemService(ConnectivityManager::class.java)?.unregisterNetworkCallback(callback)
      } catch (e: RuntimeException) {
        Log.w(TAG, "Couldn't unregister the network callback", e)
      }
    }
    networkCallback = null
    getSystemService(AlarmManager::class.java)?.cancel(RingReceiver.watchdogIntent(this))
    for (id in posted) RingNotifications.cancelIncoming(this, id)
    posted.clear()
    ringing.clear()
    since.clear()
    claimed.clear()
    answered.clear()
    declined.clear()
    silenced.clear()
    if (wakeLock.isHeld) wakeLock.release()
    super.onDestroy()
  }

  internal fun shutdown() {
    // Before startForeground, onStartCommand stops it (the session is gone).
    if (foreground) stopSelf()
  }

  // ---------- the socket ----------

  override fun onFrame(frame: JSONObject) {
    when (frame.optString("type")) {
      "call_incoming" -> frame.optJSONObject("call")?.let { RingingCall.from(it) }?.let { incoming(it) }
      "call_ended" -> ended(frame.optString("call_id"), frame.optString("reason"))
    }
  }

  override fun onUnauthorized() {
    // Logged out, or the session expired: wait for the app to sign in again.
    RingStore.clear(this)
    stopSelf()
  }

  private fun incoming(call: RingingCall) {
    if (!RingNotifications.allowed(this)) {
      // Notifications were turned off while the service ran.
      shutdown()
      return
    }
    if (call.id in declined) {
      // The decline didn't reach the server before the socket dropped.
      sendDecline(call.id)
      return
    }
    ringing[call.id] = call
    since.getOrPut(call.id) { SystemClock.elapsedRealtime() }
    holdAwake()
    refresh(call.id)
  }

  private fun ended(callId: String, reason: String) {
    val call = ringing[callId]
    val wasAnswered = callId in answered
    forget(callId)
    val missed = reason == "cancelled" || reason == "no_answer"
    if (call != null && missed && !wasAnswered && !AppVisibility.visible) {
      RingNotifications.postMissed(this, call)
    }
  }

  private fun forget(callId: String) {
    ringing.remove(callId)
    since.remove(callId)
    answered.remove(callId)
    claimed.remove(callId)
    declined.remove(callId)
    silenced.remove(callId)
    posted.remove(callId)
    // Also clears a notification left by an earlier run of the process.
    RingNotifications.cancelIncoming(this, callId)
  }

  private fun sendDecline(callId: String) {
    socket?.send(JSONObject().put("type", "call_decline").put("call_id", callId))
  }

  /** Puts up or takes down the call's ringing notification. */
  private fun refresh(callId: String) {
    val first = since[callId]
    if (first != null && SystemClock.elapsedRealtime() - first > STALE_MS) {
      forget(callId)
      return
    }
    val call = ringing[callId]
    val wanted = call != null && callId !in claimed && callId !in answered && callId !in declined
    if (wanted && callId !in posted && !AppVisibility.visible) {
      if (RingNotifications.postIncoming(this, call!!, silent = callId in silenced)) posted += callId
    } else if (!wanted && posted.remove(callId)) {
      RingNotifications.cancelIncoming(this, callId)
    }
  }

  private fun refreshAll() {
    for (id in ringing.keys.toList()) refresh(id)
  }

  // ---------- the app (CallRingerModule, CallIntents, RingReceiver) ----------

  /** The JS rings this call on screen. False: the call was silenced, show it without ringing. */
  internal fun claim(callId: String): Boolean {
    claimed += callId
    refresh(callId)
    return callId !in silenced
  }

  /** The JS stopped ringing this call (the app left the screen, or the call stopped ringing). */
  internal fun release(callId: String) {
    if (!claimed.remove(callId)) return
    // The app keeps the screen on while a call rings, so a screen that is
    // off now was turned off by the user: silence the call, as the power
    // button does for phone calls.
    if (!power.isInteractive) silenced += callId
    refresh(callId)
  }

  /** The JS went away (reload, or the app's process is ending). */
  internal fun releaseAll() {
    claimed.clear()
    refreshAll()
  }

  /** Answer on the notification: the app is opening to answer. */
  internal fun answering(callId: String) {
    answered += callId
    refresh(callId)
  }

  /** Decline on the notification. */
  internal fun decline(callId: String) {
    declined += callId
    refresh(callId)
    RingNotifications.cancelIncoming(this, callId)
    sendDecline(callId)
  }

  // ---------- keeping the link up ----------

  /** The alarm (RingReceiver): reconnects a socket that is down or went quiet. */
  internal fun watchdog() {
    val s = socket
    if (s != null && (!s.connected || SystemClock.elapsedRealtime() - s.lastHeard > QUIET_MS)) {
      holdAwake()
      s.restart()
    }
    armWatchdog()
  }

  private fun armWatchdog() {
    getSystemService(AlarmManager::class.java)?.setAndAllowWhileIdle(
      AlarmManager.ELAPSED_REALTIME_WAKEUP,
      SystemClock.elapsedRealtime() + WATCHDOG_MS,
      RingReceiver.watchdogIntent(this)
    )
  }

  private fun watchNetwork() {
    val connectivity = getSystemService(ConnectivityManager::class.java) ?: return
    val callback = object : ConnectivityManager.NetworkCallback() {
      override fun onAvailable(network: Network) {
        main.post { networkAvailable(network) }
      }
    }
    try {
      connectivity.registerDefaultNetworkCallback(callback)
      networkCallback = callback
    } catch (e: RuntimeException) {
      Log.w(TAG, "Couldn't watch the network", e)
    }
  }

  private fun networkAvailable(next: Network) {
    val previous = network
    network = next
    val s = socket ?: return
    if (previous != null && previous != next) {
      // A socket on the old network may look open but is dead.
      holdAwake()
      s.restart()
    } else {
      s.retryNow()
    }
  }

  /** Keeps the CPU up while a call posts or the socket reconnects. */
  private fun holdAwake() {
    wakeLock.acquire(AWAKE_MS)
  }

  private fun listenerUrl(config: RingStore.Config): String {
    val separator = if (config.socketUrl.contains('?')) '&' else '?'
    return "${config.socketUrl}${separator}mode=calls&token=${Uri.encode(config.token)}"
  }
}
