import { useEffect, useState } from "react"
import { onAuthStateChanged, signOut, type User } from "firebase/auth"

import { devBypass, firebaseAuth } from "@/lib/firebase"
import { Items } from "@/pages/Items"
import { Login } from "@/pages/Login"

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [ready, setReady] = useState(devBypass)

  useEffect(() => {
    if (devBypass) return
    return onAuthStateChanged(firebaseAuth(), (u) => {
      setUser(u)
      setReady(true)
    })
  }, [])

  if (!ready) return null
  if (devBypass) return <Items onSignOut={() => location.reload()} />
  if (!user) return <Login />
  return <Items onSignOut={() => void signOut(firebaseAuth())} />
}
