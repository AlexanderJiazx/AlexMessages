package expo.modules.callringer

import android.app.Activity
import android.app.Application
import android.content.Context
import android.content.Intent
import android.os.Bundle
import expo.modules.core.interfaces.ApplicationLifecycleListener
import expo.modules.core.interfaces.Package
import expo.modules.core.interfaces.ReactActivityLifecycleListener
import java.lang.ref.WeakReference

/**
 * Hooks the ring service needs outside the module: app visibility from
 * Application.onCreate (also when the process starts for the service alone),
 * and the activity's intents for notification taps.
 */
class CallRingerPackage : Package {
  override fun createApplicationLifecycleListeners(context: Context): List<ApplicationLifecycleListener> =
    listOf(
      object : ApplicationLifecycleListener {
        override fun onCreate(application: Application) {
          AppVisibility.install(application)
        }
      }
    )

  override fun createReactActivityLifecycleListeners(activityContext: Context): List<ReactActivityLifecycleListener> =
    listOf(CallIntentListener())
}

private class CallIntentListener : ReactActivityLifecycleListener {
  private var activity: WeakReference<Activity>? = null

  override fun onCreate(activity: Activity, savedInstanceState: Bundle?) {
    this.activity = WeakReference(activity)
    CallIntents.handle(activity, activity.intent, fresh = savedInstanceState == null)
  }

  override fun onNewIntent(intent: Intent?): Boolean {
    val activity = activity?.get() ?: return false
    return CallIntents.handle(activity, intent, fresh = true)
  }
}
