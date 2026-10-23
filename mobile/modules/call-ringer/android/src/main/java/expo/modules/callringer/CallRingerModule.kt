package expo.modules.callringer

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import expo.modules.kotlin.Promise
import expo.modules.kotlin.exception.Exceptions
import expo.modules.kotlin.functions.Queues
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition

/** The JS side of the ring service (modules/call-ringer/index.android.ts). */
class CallRingerModule : Module() {
  private val context: Context
    get() = appContext.reactContext ?: throw Exceptions.ReactContextLost()

  override fun definition() = ModuleDefinition {
    Name("CallRinger")

    Events("onCallAction")

    OnStartObserving("onCallAction") {
      CallIntents.listener = { tap -> sendEvent("onCallAction", tap.toMap()) }
    }

    OnStopObserving("onCallAction") {
      CallIntents.listener = null
    }

    OnDestroy {
      CallIntents.listener = null
      RingService.onMain { it.releaseAll() }
    }

    Function("isRunning") {
      RingService.instance != null
    }

    AsyncFunction("start") { socketUrl: String, token: String ->
      RingService.start(context, RingStore.Config(socketUrl, token))
    }.runOnQueue(Queues.MAIN)

    AsyncFunction("stop") {
      RingService.stop(context)
    }.runOnQueue(Queues.MAIN)

    AsyncFunction("claim") { callId: String ->
      RingService.instance?.claim(callId) ?: true
    }.runOnQueue(Queues.MAIN)

    AsyncFunction("release") { callId: String ->
      RingService.instance?.release(callId)
      Unit
    }.runOnQueue(Queues.MAIN)

    Function("takeLaunchAction") {
      CallIntents.take()?.toMap()
    }

    AsyncFunction("dismissMissed") { channel: String ->
      RingNotifications.cancelMissed(context, channel)
    }

    Function("notificationsAllowed") {
      RingNotifications.allowed(context)
    }

    AsyncFunction("openNotificationSettings") {
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        launch(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
      } else {
        launch(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, packageUri()))
      }
    }

    Function("canUseFullScreenIntent") {
      if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
        true
      } else {
        (context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager).canUseFullScreenIntent()
      }
    }

    AsyncFunction("openFullScreenIntentSettings") {
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
        launch(Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, packageUri()))
      }
    }

    Function("isIgnoringBatteryOptimizations") {
      (context.getSystemService(Context.POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(context.packageName)
    }

    AsyncFunction("requestIgnoreBatteryOptimizations") {
      // Not a Play Store app: the direct system prompt is allowed.
      launch(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, packageUri()))
    }

    AsyncFunction("setShowWhenLocked") { on: Boolean ->
      appContext.currentActivity?.let { LockScreen.set(it, on) }
      Unit
    }.runOnQueue(Queues.MAIN)

    Function("isDeviceLocked") {
      appContext.currentActivity?.let { LockScreen.locked(it) } ?: false
    }

    AsyncFunction("requestUnlock") { promise: Promise ->
      val activity = appContext.currentActivity
      if (activity == null) {
        promise.resolve(false)
      } else {
        LockScreen.requestUnlock(activity) { promise.resolve(it) }
      }
    }.runOnQueue(Queues.MAIN)
  }

  private fun packageUri(): Uri = Uri.parse("package:${context.packageName}")

  private fun launch(intent: Intent) {
    val activity = appContext.currentActivity
    if (activity != null) {
      activity.startActivity(intent)
    } else {
      context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
  }
}
