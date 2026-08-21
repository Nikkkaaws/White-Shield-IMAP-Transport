package io.whiteshield.wsit

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class SecureConfigStore(context: Context) {
    private val appContext = context.applicationContext
    private val preferences = appContext.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    fun load(): AppConfig {
        val encoded = preferences.getString(CONFIG, null) ?: return AppConfig()
        return runCatching {
            val packed = Base64.decode(encoded, Base64.NO_WRAP)
            require(packed.size > IV_SIZE)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, packed.copyOfRange(0, IV_SIZE)))
            val plain = cipher.doFinal(packed.copyOfRange(IV_SIZE, packed.size))
            AppConfig.fromJSON(JSONObject(String(plain, Charsets.UTF_8)))
        }.getOrElse { AppConfig() }
    }

    fun save(config: AppConfig) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val plain = config.toStorageJSON().toString().toByteArray(Charsets.UTF_8)
        val encrypted = cipher.doFinal(plain)
        val packed = cipher.iv + encrypted
        preferences.edit().putString(CONFIG, Base64.encodeToString(packed, Base64.NO_WRAP)).apply()
    }

    fun setTransportRequested(requested: Boolean) {
        preferences.edit().putBoolean(TRANSPORT_REQUESTED, requested).apply()
    }

    fun transportRequested(): Boolean = preferences.getBoolean(TRANSPORT_REQUESTED, false)

    private fun key(): SecretKey {
        val keyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    companion object {
        private const val PREFERENCES = "wsit_secure"
        private const val CONFIG = "encrypted_config"
        private const val TRANSPORT_REQUESTED = "transport_requested"
        private const val KEYSTORE = "AndroidKeyStore"
        private const val KEY_ALIAS = "wsit-config-v1"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val IV_SIZE = 12
    }
}
