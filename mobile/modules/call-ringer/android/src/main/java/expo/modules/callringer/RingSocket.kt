package expo.modules.callringer

import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.util.concurrent.TimeUnit
import kotlin.random.Random

/**
 * The call listener socket (/ws?mode=calls, internal/webapp/call_listener.go)
 * with reconnects: 1 s doubling to 60 s, plus jitter. The server sends the
 * heartbeat (the phone sleeps between packets), so this answers each ping
 * and records when it last heard anything; RingService's watchdog reconnects
 * a socket that went quiet. Main thread only.
 */
internal class RingSocket(private val url: String, private val listener: Listener) {
  interface Listener {
    fun onFrame(frame: JSONObject)

    /** Close 4401: the session is gone (logged out). */
    fun onUnauthorized()
  }

  private val main = Handler(Looper.getMainLooper())
  private val client = OkHttpClient.Builder()
    .connectTimeout(15, TimeUnit.SECONDS)
    .readTimeout(0, TimeUnit.MILLISECONDS)
    .build()
  private var socket: WebSocket? = null
  private var attempt = 0
  private var closed = false
  private val reconnect = Runnable { open() }

  /** The handshake finished on the current socket. */
  var connected = false
    private set

  /** SystemClock.elapsedRealtime() of the last frame. */
  var lastHeard = 0L
    private set

  fun connect() {
    if (!closed && socket == null) open()
  }

  /** Reconnects now: the network changed, or the link went quiet. */
  fun restart() {
    if (closed) return
    main.removeCallbacks(reconnect)
    socket?.cancel()
    socket = null
    connected = false
    attempt = 0
    open()
  }

  /** Skips the backoff wait (the network came back). No-op while a socket is open. */
  fun retryNow() {
    if (closed || socket != null) return
    attempt = 0
    open()
  }

  fun send(frame: JSONObject): Boolean = socket?.send(frame.toString()) ?: false

  fun close() {
    closed = true
    main.removeCallbacks(reconnect)
    socket?.close(1000, null)
    socket = null
    connected = false
  }

  private fun open() {
    if (closed) return
    main.removeCallbacks(reconnect)
    val request = Request.Builder().url(url).build()
    socket = client.newWebSocket(request, object : WebSocketListener() {
      override fun onOpen(webSocket: WebSocket, response: Response) {
        main.post {
          if (socket !== webSocket) return@post
          connected = true
          attempt = 0
          lastHeard = SystemClock.elapsedRealtime()
        }
      }

      override fun onMessage(webSocket: WebSocket, text: String) {
        main.post {
          if (socket !== webSocket) return@post
          lastHeard = SystemClock.elapsedRealtime()
          val frame = try {
            JSONObject(text)
          } catch (e: Exception) {
            return@post
          }
          if (frame.optString("type") == "ping") {
            webSocket.send("""{"type":"pong"}""")
          } else {
            listener.onFrame(frame)
          }
        }
      }

      override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
        webSocket.close(code, null)
        main.post { dropped(webSocket, code) }
      }

      override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
        main.post { dropped(webSocket, code) }
      }

      override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
        main.post { dropped(webSocket, 0) }
      }
    })
  }

  private fun dropped(webSocket: WebSocket, code: Int) {
    if (socket !== webSocket) return
    socket = null
    connected = false
    if (closed) return
    if (code == 4401) {
      listener.onUnauthorized()
      return
    }
    val delay = minOf(60_000L, 1_000L shl minOf(attempt, 6)) + Random.nextLong(0, 1_000)
    attempt++
    main.postDelayed(reconnect, delay)
  }
}
