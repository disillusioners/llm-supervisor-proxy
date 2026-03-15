# Common Golang Traps (Quick Guide for Backend Teams)

A short list of common pitfalls that frequently cause bugs in Go
production systems.

------------------------------------------------------------------------

## 1. Loop Variable Capture in Goroutines

*Note: This was fixed natively in Go 1.22! `for` loops now create variables per-iteration instead of per-loop.*

**Problem (Go <= 1.21)**

``` go
for _, user := range users {
    go func() {
        fmt.Println(user.Name) // All goroutines may print the *same* value
    }()
}
```

**Fix (Go <= 1.21)**

``` go
for _, user := range users {
    u := user
    go func() {
        fmt.Println(u.Name)
    }()
}
}
```

------------------------------------------------------------------------

## 2. Interface `nil` Trap

``` go
var err *MyError = nil
return err
```

Caller:

``` go
if err != nil { }
```

This condition **may be true** because interfaces store `(type, value)`.

**Fix**

Return `nil` directly.

------------------------------------------------------------------------

## 3. `defer` Inside Loops

``` go
for _, file := range files {
    f, _ := os.Open(file)
    defer f.Close()
}
```

Files stay open until function exit.

**Fix**

Close explicitly or wrap in a function.

------------------------------------------------------------------------

## 4. `time.After` in Infinite Loops

*Note: **No longer a memory leak risk in Go 1.23+**. But for Go <= 1.22, it was a major issue.*

``` go
for {
    select {
    case <-time.After(time.Second):
    }
}
```

Creates a new timer every iteration on the heap until it fires.

**Fix**

``` go
ticker := time.NewTicker(time.Second)
defer ticker.Stop()
```

------------------------------------------------------------------------

## 5. Goroutine Leaks

``` go
go worker(ch)
```

If the channel is never closed or canceled, the goroutine may live
forever.

**Best practice**

Use `context.Context` and proper shutdown signals.

------------------------------------------------------------------------

## 6. Maps Are Not Thread‑Safe

``` go
m := map[string]int{}
go func(){ m["a"] = 1 }()
go func(){ m["b"] = 2 }()
```

Leads to:

    fatal error: concurrent map writes

**Fix**

Use:

-   `sync.Mutex`
-   `sync.RWMutex`
-   `sync.Map`

------------------------------------------------------------------------

## 7. Slice `append` Reallocation

``` go
func add(s []int) {
    s = append(s, 10)
}
```

Caller may not see the change.

**Fix**

Return the slice.

``` go
func add(s []int) []int {
    return append(s, 10)
}
```

------------------------------------------------------------------------

## 8. Ignoring Context

Always propagate context in I/O operations.

Bad:

``` go
http.NewRequest("GET", url, nil)
```

Good:

``` go
http.NewRequestWithContext(ctx, "GET", url, nil)
```

------------------------------------------------------------------------

## 9. HTTP Response Body Handling

A common misunderstanding is that `resp.Body` must be checked and closed even when `err != nil`. **This is false.** 

According to the official `net/http` package documentation: "On error, any Response can be ignored. A non-nil Response with a non-nil error only occurs when CheckRedirect fails, **and even then the returned Response.Body is already closed**."

**Correct Handling**

``` go
resp, err := client.Do(req)
if err != nil {
    return err // No need to close resp.Body here
}
defer resp.Body.Close() // ONLY defer close if err == nil
```

Failing to close the body on *success* → **connection leaks**.

------------------------------------------------------------------------

## 10. Panic Inside Goroutines

``` go
go func() {
    panic("boom")
}()
```

May crash the entire service.

**Best practice**

Wrap goroutines with `recover()`.

------------------------------------------------------------------------

# Quick Production Rules

1.  Always close HTTP bodies (unless there is an error).
2.  Always propagate `context.Context`.
3.  Avoid `time.After` in loops.
4.  Protect shared maps.
5.  Watch for goroutine leaks.
6.  Be careful with `range` variable capture.

------------------------------------------------------------------------

This document is meant as a **quick checklist for Go backend teams**.
