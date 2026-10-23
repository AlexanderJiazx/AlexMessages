package expo.modules.callringer

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.net.Uri
import android.os.Build
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import org.json.JSONObject

/** A call ringing for this phone, from its call_incoming frame. */
internal data class RingingCall(val id: String, val name: String, val video: Boolean, val channel: String) {
  companion object {
    fun from(call: JSONObject): RingingCall? {
      val id = call.optString("id").takeIf { it.isNotEmpty() } ?: return null
      val peer = call.optJSONObject("peer")
      val name = peer?.optString("display_name")?.takeIf { it.isNotEmpty() }
        ?: peer?.optString("username")?.takeIf { it.isNotEmpty() }
        ?: "Alex Messages"
      return RingingCall(id, name, call.optString("media") == "video", call.optString("channel"))
    }
  }
}

/**
 * The ring service's notifications:
 *  - "Ready for calls" (the foreground service's own, minimized),
 *  - the incoming call: CallStyle with Decline / Answer, a full-screen
 *    intent (over the lock screen), and the ringtone on its channel,
 *    repeating (FLAG_INSISTENT) until the call stops ringing. The system
 *    applies the ringer mode and Do Not Disturb to it. A silenced call is
 *    posted with setSilent: no sound, and (Android 13+) no full-screen
 *    launch, since SystemUI skips the full-screen intent of a notification
 *    whose group alert behavior suppresses it.
 *  - the missed call, when the app isn't running to post it itself.
 */
internal object RingNotifications {
  private const val CHANNEL_SERVICE = "am_call_service"
  private const val CHANNEL_INCOMING = "am_incoming_calls"
  private const val CHANNEL_MISSED = "am_missed_calls"

  const val ID_SERVICE = 7101
  private const val ID_INCOMING = 7102
  // A missed call takes its conversation's notification slot: tag = the
  // channel, id 0, where expo-notifications posts the app's own
  // notification for that conversation (identifier = the channel, id
  // ExpoPresentationDelegate.ANDROID_NOTIFICATION_ID). Either replaces the
  // other, so a conversation shows one notification.
  private const val ID_MISSED = 0

  /** Notification intents into the app (read by CallIntents). */
  const val ACTION_CALL = "expo.modules.callringer.CALL"
  const val EXTRA_ACTION = "am_call_action"
  const val EXTRA_CALL_ID = "am_call_id"
  const val EXTRA_CHANNEL = "am_channel"

  /** An unanswered call rings for 45 s on the server; this is the backstop. */
  private const val RING_LIMIT_MS = 60_000L
  private val VIBRATION = longArrayOf(0, 1000, 1000)

  /** A call can ring: the app may post notifications, and its incoming-call category is on. */
  fun allowed(context: Context): Boolean {
    val manager = NotificationManagerCompat.from(context)
    if (!manager.areNotificationsEnabled()) return false
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return true
    val channel = manager.getNotificationChannel(CHANNEL_INCOMING) ?: return true
    return channel.importance != NotificationManager.IMPORTANCE_NONE
  }

  fun createChannels(context: Context) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val manager = context.getSystemService(NotificationManager::class.java) ?: return
    manager.createNotificationChannel(
      NotificationChannel(CHANNEL_SERVICE, "Ready for calls", NotificationManager.IMPORTANCE_MIN).apply {
        description = "Shown while Alex Messages listens for calls with the app closed."
        setShowBadge(false)
      }
    )
    manager.createNotificationChannel(
      NotificationChannel(CHANNEL_INCOMING, "Incoming calls", NotificationManager.IMPORTANCE_HIGH).apply {
        description = "Rings for voice and video calls."
        setSound(ringtone(context), ringtoneAttributes())
        enableVibration(true)
        vibrationPattern = VIBRATION
        lockscreenVisibility = Notification.VISIBILITY_PUBLIC
        setShowBadge(false)
      }
    )
    manager.createNotificationChannel(
      NotificationChannel(CHANNEL_MISSED, "Missed calls", NotificationManager.IMPORTANCE_DEFAULT)
    )
  }

  fun service(context: Context): Notification =
    NotificationCompat.Builder(context, CHANNEL_SERVICE)
      .setSmallIcon(R.drawable.am_ring_stat)
      .setContentTitle("Ready for calls")
      .setContentText("Calls ring even with Alex Messages closed")
      .setOngoing(true)
      .setShowWhen(false)
      .setPriority(NotificationCompat.PRIORITY_MIN)
      .setCategory(NotificationCompat.CATEGORY_SERVICE)
      .setContentIntent(appIntent(context, REQUEST_APP, "app", null, null))
      .build()

  /** Rings (or, silent, only shows the call). False when it couldn't post. */
  fun postIncoming(context: Context, call: RingingCall, silent: Boolean): Boolean {
    val person = Person.Builder().setName(call.name).setImportant(true).build()
    val open = appIntent(context, REQUEST_OPEN, "open", call.id, call.channel)
    val answer = appIntent(context, REQUEST_ANSWER, "answer", call.id, call.channel)
    val decline = PendingIntent.getBroadcast(
      context,
      REQUEST_DECLINE,
      Intent(context, RingReceiver::class.java)
        .setAction(RingReceiver.ACTION_DECLINE)
        .putExtra(EXTRA_CALL_ID, call.id),
      PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
    )
    val notification = NotificationCompat.Builder(context, CHANNEL_INCOMING)
      .setSmallIcon(R.drawable.am_ring_stat)
      .setContentTitle(call.name)
      .setContentText(if (call.video) "Incoming video call" else "Incoming voice call")
      .setCategory(NotificationCompat.CATEGORY_CALL)
      .setPriority(NotificationCompat.PRIORITY_MAX)
      .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
      .setOngoing(true)
      .setAutoCancel(false)
      .setContentIntent(open)
      .setFullScreenIntent(open, true)
      .setStyle(NotificationCompat.CallStyle.forIncomingCall(person, decline, answer).setIsVideo(call.video))
      .setTimeoutAfter(RING_LIMIT_MS)
      // Before Android 8 the sound and vibration ride the notification.
      .setSound(ringtone(context))
      .setVibrate(VIBRATION)
      .setSilent(silent)
      .build()
    if (!silent) notification.flags = notification.flags or Notification.FLAG_INSISTENT
    return notify(context, call.id, ID_INCOMING, notification)
  }

  fun cancelIncoming(context: Context, callId: String) {
    NotificationManagerCompat.from(context).cancel(callId, ID_INCOMING)
  }

  fun postMissed(context: Context, call: RingingCall) {
    val notification = NotificationCompat.Builder(context, CHANNEL_MISSED)
      .setSmallIcon(R.drawable.am_ring_stat)
      .setContentTitle(call.name)
      .setContentText(if (call.video) "Missed video call" else "Missed voice call")
      .setCategory(NotificationCompat.CATEGORY_MISSED_CALL)
      .setAutoCancel(true)
      .setContentIntent(appIntent(context, missedRequest(call.channel), "chat", call.id, call.channel))
      .build()
    // One per conversation, like the app's message notifications.
    notify(context, call.channel, ID_MISSED, notification)
  }

  fun cancelMissed(context: Context, channel: String) {
    NotificationManagerCompat.from(context).cancel(channel, ID_MISSED)
  }

  private fun notify(context: Context, tag: String, id: Int, notification: Notification): Boolean {
    val manager = NotificationManagerCompat.from(context)
    if (!manager.areNotificationsEnabled()) return false
    return try {
      manager.notify(tag, id, notification)
      true
    } catch (e: RuntimeException) {
      // No POST_NOTIFICATIONS grant, or the system refused the notification.
      Log.w(TAG, "Couldn't post the call notification", e)
      false
    }
  }

  /** Opens the app's activity (singleTask: onNewIntent when it runs). */
  private fun appIntent(context: Context, request: Int, action: String, callId: String?, channel: String?): PendingIntent {
    val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)
    val intent = Intent(ACTION_CALL)
      .setComponent(launch?.component ?: ComponentName(context.packageName, "${context.packageName}.MainActivity"))
      .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
      .putExtra(EXTRA_ACTION, action)
    callId?.let { intent.putExtra(EXTRA_CALL_ID, it) }
    channel?.let { intent.putExtra(EXTRA_CHANNEL, it) }
    return PendingIntent.getActivity(context, request, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
  }

  /** The app's ringtone (assets/sounds, copied to res/raw by expo-asset). */
  private fun ringtone(context: Context): Uri =
    Uri.parse("android.resource://${context.packageName}/raw/incallmanager_ringtone")

  private fun ringtoneAttributes(): AudioAttributes =
    AudioAttributes.Builder()
      .setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE)
      .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
      .build()

  private const val REQUEST_APP = 1
  private const val REQUEST_OPEN = 2
  private const val REQUEST_ANSWER = 3
  private const val REQUEST_DECLINE = 4

  /**
   * One PendingIntent per conversation: intents that differ only in their
   * extras are the same PendingIntent, and FLAG_UPDATE_CURRENT would point
   * every missed call at the newest conversation.
   */
  private fun missedRequest(channel: String) = 0x10000 or (channel.hashCode() and 0xffff)

  private const val TAG = "CallRinger"
}
