# Edges, one per construct

## Emphasis and code

*one* **two** ***three*** _under_ __double__ a*b*c `code` ``a ` b`` \*escaped\*

## Headings with the same text

### Setup

### Setup

## Line breaks

a line
followed by another

two spaces at the end  
then a hard break

## Links

[text](https://example.com "title") <https://example.com> www.example.com
https://example.com/path?q=1&r=2 mail@example.com [ref][r]

[r]: https://example.org

## Strikethrough and tasks

~~gone~~ ~one tilde~

- [ ] open
- [x] done

## Table

| Left | Centre | Right |
|:-----|:------:|------:|
| a    | b      | c     |
| `x|y` | **bold** | 3 |

## Lists

1. one
2. two
   - nested
     - deeper
3. three

- tight
- list

- loose

- list

## Quote and fence

> quoted
> > nested

```go
func main() { println("<tag>") }
```

    indented code

## Footnote

A claim.[^1] Another.[^note]

[^1]: The source.
[^note]: A named one.

## Raw HTML and entities

<div class="raw">raw</div> <script>alert(1)</script>

&amp; &copy; &#169; &#xA9; < > "quotes" 'single'

## Unicode

नमस्ते, 你好, émoji ✓ — dash … ellipsis

---

Final paragraph.
