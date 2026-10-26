import { useEffect, useState } from "react"
import { Trash2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api, type Item } from "@/lib/api"

export function Items({ onSignOut }: { onSignOut: () => void }) {
  const [items, setItems] = useState<Item[]>([])
  const [title, setTitle] = useState("")
  const [error, setError] = useState("")

  async function load() {
    try {
      setItems(await api.items())
    } catch (err) {
      setError((err as Error).message)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function add(e: React.FormEvent) {
    e.preventDefault()
    if (!title.trim()) return
    await api.createItem(title.trim())
    setTitle("")
    await load()
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">__APP__ admin</h1>
        <Button variant="ghost" onClick={onSignOut}>
          Sign out
        </Button>
      </header>
      <Card>
        <CardHeader>
          <CardTitle>Items</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <form onSubmit={add} className="flex gap-2">
            <Input placeholder="New item" value={title} onChange={(e) => setTitle(e.target.value)} />
            <Button type="submit">Add</Button>
          </form>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-12">Done</TableHead>
                <TableHead>Title</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((it) => (
                <TableRow key={it.id}>
                  <TableCell>
                    <input
                      type="checkbox"
                      checked={it.done}
                      onChange={async (e) => {
                        await api.setDone(it.id, e.target.checked)
                        await load()
                      }}
                    />
                  </TableCell>
                  <TableCell className={it.done ? "text-muted-foreground line-through" : ""}>{it.title}</TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="Delete"
                      onClick={async () => {
                        await api.deleteItem(it.id)
                        await load()
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
