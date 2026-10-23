package expo.modules.callringer

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Where the ring service finds its server socket and session after a reboot or a
 * restart without the app: the session token is sealed with an AES-GCM key
 * that never leaves the Android Keystore (EncryptedSharedPreferences is
 * deprecated). A backup restored to another phone can't open it, and is
 * dropped.
 */
internal object RingStore {
  /** socketUrl is the server's /ws URL (ws:// or wss://). */
  data class Config(val socketUrl: String, val token: String)

  private const val PREFS = "am_call_ringer"
  private const val KEY_ALIAS = "am_call_ringer_token"
  private const val GCM_TAG_BITS = 128

  fun save(context: Context, config: Config) {
    val cipher = Cipher.getInstance("AES/GCM/NoPadding")
    cipher.init(Cipher.ENCRYPT_MODE, key())
    val sealed = cipher.doFinal(config.token.toByteArray(Charsets.UTF_8))
    prefs(context).edit()
      .putString("socket", config.socketUrl)
      .putString("iv", encode(cipher.iv))
      .putString("token", encode(sealed))
      .apply()
  }

  fun load(context: Context): Config? {
    val prefs = prefs(context)
    val socket = prefs.getString("socket", null) ?: return null
    val iv = prefs.getString("iv", null) ?: return null
    val sealed = prefs.getString("token", null) ?: return null
    return try {
      val cipher = Cipher.getInstance("AES/GCM/NoPadding")
      cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(GCM_TAG_BITS, decode(iv)))
      Config(socket, String(cipher.doFinal(decode(sealed)), Charsets.UTF_8))
    } catch (e: Exception) {
      clear(context)
      null
    }
  }

  fun clear(context: Context) {
    prefs(context).edit().clear().apply()
  }

  private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

  private fun key(): SecretKey {
    val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    (store.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
    val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
    generator.init(
      KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
        .setKeySize(256)
        .build()
    )
    return generator.generateKey()
  }

  private fun encode(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.NO_WRAP)

  private fun decode(text: String) = Base64.decode(text, Base64.NO_WRAP)
}
