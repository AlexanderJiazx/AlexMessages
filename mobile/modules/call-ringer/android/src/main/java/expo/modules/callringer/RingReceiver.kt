package expo.modules.callringer

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Decline on the ringing notification, the ring service's watchdog alarm,
 * and the restarts after a reboot or an app update.
 */
class RingReceiver : BroadcastReceiver() {
  companion object {
    const val ACTION_DECLINE = "expo.modules.callringer.DECLINE"
    private const val ACTION_WATCHDOG = "expo.modules.callringer.WATCHDOG"

    fun watchdogIntent(context: Context): PendingIntent =
      PendingIntent.getBroadcast(
        context,
        0,
        Intent(context, RingReceiver::class.java).setAction(ACTION_WATCHDOG),
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
      )
  }

  override fun onReceive(context: Context, intent: Intent) {
    when (intent.action) {
      ACTION_DECLINE -> {
        val callId = intent.getStringExtra(RingNotifications.EXTRA_CALL_ID) ?: return
        val service = RingService.instance
        if (service != null) {
          service.decline(callId)
        } else {
          // Left by an earlier run of the process: nothing can decline it now.
          RingNotifications.cancelIncoming(context, callId)
        }
      }
      ACTION_WATCHDOG -> {
        val service = RingService.instance
        if (service != null) service.watchdog() else RingService.revive(context)
      }
      Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> RingService.revive(context)
    }
  }
}
