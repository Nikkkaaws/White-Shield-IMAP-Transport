plugins {
    id("com.android.application")
}

android {
    namespace = "io.whiteshield.wsit"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.whiteshield.wsit"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            isShrinkResources = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    packaging {
        jniLibs.useLegacyPackaging = true
        resources.excludes += setOf("META-INF/LICENSE", "META-INF/NOTICE")
    }
}

dependencies {
    implementation(files("libs/wsit-mobile.aar"))
}
