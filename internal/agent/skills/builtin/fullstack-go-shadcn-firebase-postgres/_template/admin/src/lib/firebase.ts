import { initializeApp, type FirebaseApp } from "firebase/app"
import { getAuth, type Auth } from "firebase/auth"

const config = {
  apiKey: import.meta.env.VITE_FIREBASE_API_KEY,
  authDomain: import.meta.env.VITE_FIREBASE_AUTH_DOMAIN,
  projectId: import.meta.env.VITE_FIREBASE_PROJECT_ID,
}

// devBypass signs in with the backend's AUTH_DEV_BYPASS token instead of Firebase.
export const devBypass = import.meta.env.VITE_AUTH_DEV_BYPASS === "1" || !config.apiKey

let app: FirebaseApp | undefined
let auth: Auth | undefined

export function firebaseAuth(): Auth {
  if (!auth) {
    app = initializeApp(config)
    auth = getAuth(app)
  }
  return auth
}
