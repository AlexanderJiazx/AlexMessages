package expo.modules.callringer

import android.app.Activity
import android.app.Application
import android.os.Bundle
import java.lang.ref.WeakReference

/**
 * Whether an activity of the app is resumed, for the whole process. This is
 * the signal React Native's AppState follows ("active" from onResume to
 * onPause), so the ring service and the JS agree on when the app is on
 * screen. Installed from Application.onCreate (CallRingerPackage), before
 * any activity starts. Main thread only.
 */
internal object AppVisibility : Application.ActivityLifecycleCallbacks {
  private var resumed = 0
  private var installed = false
  private var current: WeakReference<Activity>? = null

  val visible: Boolean get() = resumed > 0

  /** The app's newest activity, while it exists (for the lock screen flags). */
  val activity: Activity? get() = current?.get()

  /** The app left the screen. */
  var onHidden: (() -> Unit)? = null

  fun install(application: Application) {
    if (installed) return
    installed = true
    application.registerActivityLifecycleCallbacks(this)
  }

  override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {
    current = WeakReference(activity)
  }

  override fun onActivityResumed(activity: Activity) {
    current = WeakReference(activity)
    resumed++
  }

  override fun onActivityPaused(activity: Activity) {
    if (resumed > 0) resumed--
    if (resumed == 0) onHidden?.invoke()
  }

  override fun onActivityStarted(activity: Activity) {}

  override fun onActivityStopped(activity: Activity) {}

  override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}

  override fun onActivityDestroyed(activity: Activity) {
    if (current?.get() === activity) current = null
  }
}
