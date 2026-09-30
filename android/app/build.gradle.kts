plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "dev.soundstorm.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "dev.soundstorm.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 10
        versionName = "0.10"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
        // The published APK is this build type, signed with the debug key so
        // it installs over earlier ones - but not debuggable: with it, anyone
        // with adb access could copy the sign-in cookies (run-as) and open a
        // console in the signed-in page (a security review).
        debug {
            isDebuggable = false
        }
    }

    buildFeatures {
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    // Only what the platform itself lacks: document-start scripts and origin-
    // checked messages for the web view, and the media session and its
    // notification. No AppCompat, no Compose - the screens are two.
    implementation("androidx.core:core-ktx:1.17.0")
    implementation("androidx.webkit:webkit:1.14.0")
    implementation("androidx.media:media:1.7.0")
    // Android's own media player, for songs from the server (AudioService):
    // screen-off playback, audio focus, gapless.
    implementation("androidx.media3:media3-exoplayer:1.5.1")
    implementation("androidx.media3:media3-session:1.5.1")
}
