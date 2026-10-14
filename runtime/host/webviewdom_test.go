package host

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"modernc.org/quickjs"
)

// A script must not be able to turn the DOM's child graph into a cycle by
// appending a node to one of its own descendants, or to itself. Every host
// traversal of that graph -- nodeText/renderNode (htmlnode.go:56/:163) and
// queryNodes (webviewdom.go:873) -- recurses without a bound, so a cycle
// aborts the whole process with a fatal "stack overflow" that no recover()
// can intercept. A real DOM rejects this with a HierarchyRequestError.
//
// Nodes are captured before the mutation on purpose: the append also
// detaches the ancestor's subtree from the document root, so re-querying by
// id afterwards would return null and mask the graph state being asserted.
// textContent/innerHTML are deliberately not read here -- with a cycle they
// would recurse forever and hang the test binary instead of failing.
func TestWebViewAppendChildRejectsAncestorCycle(t *testing.T) {
	page := `<!doctype html><html><body>
		<div id="a"><div id="b"><span id="c">deep</span></div></div>
	</body></html>`

	cases := []struct {
		name string
		// mutation runs inside a try/catch so an uncaught exception never
		// masks the assertion below.
		mutation string
		// want is "threw|a.children.length|b.parentElement.id|connected|exc"
		// afterwards, where connected means both nodes are still reachable
		// from the document (pre-fix the accepted append detached the whole
		// subtree into a cycle) and exc means the thrown error actually
		// named the DOM violation.
		want string
	}{
		{
			name:     "child is an ancestor of the parent",
			mutation: `b.appendChild(a);`,
			want:     "yes|1|a|connected|HierarchyRequestError",
		},
		{
			name:     "child is the parent itself",
			mutation: `a.appendChild(a);`,
			want:     "yes|1|a|connected|HierarchyRequestError",
		},
		{
			// The guard walks the whole ancestor chain, not just the direct
			// parent: c's grandparent is a.
			name:     "child is a deeper ancestor",
			mutation: `document.getElementById('c').appendChild(a);`,
			want:     "yes|1|a|connected|HierarchyRequestError",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh document per case: a rejected (or, pre-fix, accepted)
			// append mutates the tree, and these cases must not leak state
			// into each other.
			wv := newTestWebView(t)
			wv.loadHTML(page, nil)

			var got string
			wv.loop.Run(func(vm *quickjs.VM) {
				v, err := vm.Eval(`
					(() => {
						const a = document.getElementById('a');
						const b = document.getElementById('b');
						let threw = "no", exc = "none";
						try { `+tc.mutation+` } catch (e) { threw = "yes"; exc = e.message; }
						const parentId = b.parentElement ? b.parentElement.id : '-';
						const connected = (document.getElementById('a') && document.getElementById('b')) ? "connected" : "detached";
						const named = /HierarchyRequestError/.test(exc) ? "HierarchyRequestError" : exc;
						return threw + '|' + a.children.length + '|' + parentId + '|' + connected + '|' + named;
					})()
				`, quickjs.EvalGlobal)
				if err != nil {
					t.Fatalf("Eval: %v", err)
				}
				got = stringify(v)
			})
			if got != tc.want {
				t.Errorf("after %s: got %q, want %q (the append must be rejected with a DOM error, leaving the tree unchanged and reachable)",
					tc.name, got, tc.want)
			}
		})
	}
}

// End-to-end: a hostile or merely broken page's own inline <script> is
// enough to reach the cycle (webview_load runs page scripts via loadHTML),
// so the process must survive it rather than dying with a fatal stack
// overflow. Run in a child process because that abort is unrecoverable.
func TestWebViewPageScriptCannotCycleTheDOM(t *testing.T) {
	if os.Getenv("ZZ_DOM_CYCLE_CHILD") == "1" {
		wv := newTestWebView(t)
		wv.loadHTML(`<!doctype html><html><body><div id="x">page text</div><script>
			const x = document.getElementById('x');
			const b = document.createElement('div');
			try {
				x.appendChild(b);
				b.appendChild(x);   // x is b's parent: must be rejected
				x.appendChild(x);   // self-append: must be rejected
			} catch (e) {}
			x.textContent;   // recurses forever if a cycle was allowed
		</script></body></html>`, nil)
		return // only reached if the process was not aborted
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestWebViewPageScriptCannotCycleTheDOM", "-test.v")
	cmd.Env = append(os.Environ(), "ZZ_DOM_CYCLE_CHILD=1")
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "stack overflow") || err != nil {
		t.Fatalf("page-supplied script aborted the process (err=%v):\n%s", err, firstLines(string(out)))
	}
}

func firstLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return strings.Join(lines, "\n")
}
