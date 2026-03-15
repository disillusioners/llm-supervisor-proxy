# Common Memory Traps in Go (Golang)

A concise guide for engineers building high‑throughput services (APIs, proxies, streaming systems, workers).

These issues are not always "memory leaks" in the traditional sense. Most are **allocation patterns that create excessive temporary memory or retain large backing arrays**.

---

# 1. Substring Retaining Large Backing Arrays

```go
big := readLargeFile() // 10MB
small := big[:100]
```

`small` keeps the **entire 10MB backing array alive**.

### Fix

Copy the needed portion. Since Go 1.18, `strings.Clone` is the idiomatic way.

```go
small := strings.Clone(big[:100])
```

*(Pre-1.18:* `small := string([]byte(big[:100]))`*)*

---

# 2. Slice Retaining Large Arrays

```go
buf := make([]byte, 10<<20) // 10MB
chunk := buf[:100]
```

The slice still references the entire array.

### Fix

Since Go 1.21, use `slices.Clone`:

```go
chunk := slices.Clone(buf[:100])
```

*(Pre-1.21:* `chunk := append([]byte(nil), buf[:100]...)`*)*

---

# 3. Re-slicing Large Buffers in Loops

```go
for {
    data := buffer[:n]
    process(data)
}
```

If `process` stores the slice, the large buffer stays alive.

### Fix

Copy before storing.

---

# 4. `strings.Builder.String()` on Growing Builder

Calling `String()` repeatedly on a builder whose buffer keeps growing can create very large temporary strings and allocation pressure.

### Safer Pattern

Extract small chunks separately and append.

```go
var chunk strings.Builder
extract(&chunk)
acc.WriteString(chunk.String())
```

---

# 5. `bytes.Buffer.Bytes()` Escaping

```go
buf := bytes.Buffer{}
return buf.Bytes()
```

The caller now holds the full buffer memory.

### Fix

Copy before returning if buffer is large.

```go
b := append([]byte(nil), buf.Bytes()...)
```

---

# 6. Map Growth Never Shrinks

Maps grow but **do not shrink automatically**.

```go
m := map[string]*Obj{}
```

If entries are deleted, the backing storage remains.

### Fix

Rebuild the map when it becomes sparse.

```go
newMap := make(map[string]*Obj, len(m))
```

---

# 7. `sync.Pool` Misuse

Objects placed in `sync.Pool` may live longer than expected.

Common issue:

- storing very large buffers
- pool grows under burst traffic

### Rule

Only pool **objects you reuse frequently and predictably**.

---

# 8. Unbounded Goroutines

```go
go handleConn(conn)
```

If requests spike, thousands of goroutines may allocate stacks and buffers.

### Fix

Use worker pools or semaphores.

---

# 9. Large Structs Passed by Value

```go
type Big struct {
    Data [1<<20]byte
}

func process(b Big)
```

Each call copies 1MB.

### Fix

```go
func process(b *Big)
```

---

# 10. JSON Unmarshal into `map[string]interface{}`

```go
var m map[string]interface{}
json.Unmarshal(data, &m)
```

Creates many allocations and interface boxes.

### Fix

Use structs.

```go
type Req struct{}
```

---

# 11. `time.After()` in Loops

*Note: This is **no longer a memory leak risk in Go 1.23+**, as unreferenced timers are natively garbage-collected immediately. For Go 1.22 and below, however, `time.After` creates a new timer every iteration that stays on the global heap until it fires.*

```go
for {
    select {
    case <-time.After(time.Second):
    }
}
```

### Fix (Go <= 1.22)

```go
t := time.NewTimer(time.Second)
defer t.Stop()
```

Reuse it.

---

# 12. `defer` in Tight Loops

```go
for {
    defer file.Close()
}
```

Defers accumulate until function exit.

### Fix

Close explicitly.

---

# 13. Large Channel Buffers

```go
ch := make(chan []byte, 10000)
```

Buffered channels hold references to large objects.

### Fix

Keep buffers small.

---

# 14. Retaining Request Context Data

Common in HTTP middleware.

```go
cache[id] = req
```

Request objects contain:

- body buffers
- headers
- contexts

### Fix

Extract only necessary fields.

---

# 15. Global Caches Without Limits

```go
var cache = map[string]*Obj{}
```

Without eviction, memory grows forever.

### Fix

Use LRU / TTL caches.

---

# 16. Logging Large Objects

```go
log.Printf("%+v", obj)
```

Formatting large structs allocates large temporary strings.

### Fix

Log identifiers or summaries.

---

# 17. Large HTTP Response Buffers


```go
body, _ := io.ReadAll(resp.Body)
```

Loads entire response into memory.

### Fix

Stream instead.

```go
io.Copy(dst, resp.Body)
```

---

# 18. Accumulating Buffers in Streaming Systems

Common in:

- SSE
- WebSocket
- LLM streaming

Problem pattern:

```go
acc += chunk
```

If repeatedly reallocated, memory pressure increases.

### Fix

Use `strings.Builder` or `bytes.Buffer`.

---

# 19. Huge Struct Fields That Are Rarely Used

```go
type Request struct {
    RawBody []byte
}
```

If passed around widely, the large field remains referenced.

### Fix

Separate heavy fields into another structure.

---

# 20. Forgetting to Close Response Bodies

```go
resp, _ := client.Do(req)
```

Not closing the body leaks connections and buffers.

### Fix

```go
defer resp.Body.Close()
```

---

# Recommended Tools

Use these tools to detect memory problems.

```
go tool pprof
go test -bench -benchmem
GODEBUG=gctrace=1
```

Also useful:

- heap profiles
- allocation profiles
- `runtime.ReadMemStats`

---

# Key Rule

Most Go "memory leaks" are actually:

- large backing arrays retained
- excessive allocation rate
- unbounded growth

Understanding **slice, string, and buffer memory semantics** is critical when building high‑throughput services.

