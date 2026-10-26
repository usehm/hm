import { useState } from "react"
import { GoogleAuthProvider, signInWithEmailAndPassword, signInWithPopup } from "firebase/auth"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { firebaseAuth } from "@/lib/firebase"

export function Login() {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")

  async function signIn(e: React.FormEvent) {
    e.preventDefault()
    setError("")
    try {
      await signInWithEmailAndPassword(firebaseAuth(), email, password)
    } catch (err) {
      setError((err as Error).message)
    }
  }

  async function google() {
    setError("")
    try {
      await signInWithPopup(firebaseAuth(), new GoogleAuthProvider())
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Sign in</CardTitle>
          <CardDescription>Use your account to open the admin.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={signIn} className="flex flex-col gap-3">
            <Input type="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} />
            <Input type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} />
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit">Sign in</Button>
            <Button type="button" variant="outline" onClick={google}>
              Continue with Google
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
