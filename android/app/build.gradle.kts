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
        versionCode = 6
        versionName = "0.6"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
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
}
