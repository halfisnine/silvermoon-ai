// Local human-acceptance provider: fixed synthetic response, no paid upstream.
package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"marketing-preview","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"This is an isolated marketing acceptance response. No paid provider was called."},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
	})
	if err := http.ListenAndServe(":18700", nil); err != nil {
		panic(err)
	}
}
