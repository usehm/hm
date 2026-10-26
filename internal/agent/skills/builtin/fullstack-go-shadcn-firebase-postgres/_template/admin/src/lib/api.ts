import { devBypass, firebaseAuth } from "@/lib/firebase"

export type Item = { id: number; owner_id: number; title: string; done: boolean; created_at: string }

async function token(): Promise<string> {
  if (devBypass) return "dev"
  const user = firebaseAuth().currentUser
  if (!user) throw new Error("not signed in")
  return user.getIdToken()
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { Authorization: `Bearer ${await token()}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.status === 204 ? (undefined as T) : res.json()
}

export const api = {
  me: () => call<{ id: number; email: string }>("GET", "/api/me"),
  items: () => call<Item[]>("GET", "/api/items"),
  createItem: (title: string) => call<Item>("POST", "/api/items", { title }),
  setDone: (id: number, done: boolean) => call<Item>("PATCH", `/api/items/${id}`, { done }),
  deleteItem: (id: number) => call<void>("DELETE", `/api/items/${id}`),
}
