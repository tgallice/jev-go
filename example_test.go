package jev_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	jev "github.com/tgallice/jev-go"
)

func ExampleNew() {
	client, err := jev.New(
		jev.WithAPIKey("test"),
		jev.WithBaseURL("https://api.typesafe.ai"),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(client.Model())
	fmt.Println(client.BaseURL())
	// Output:
	// jev-latest
	// https://api.typesafe.ai
}

func ExampleClient_Evaluate() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{
			"department":{"type":"choice","choice":"technical","probabilities":{"technical":0.82,"billing":0.18},"confidence":0.82}
		},"usage":{"input_tokens":42,"output_tokens":7}}`)
	}))
	defer srv.Close()

	client, err := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(srv.URL))
	if err != nil {
		fmt.Println(err)
		return
	}

	type Dept string

	req := &jev.Request{State: "Our integration returns 500 on every request."}
	dept := jev.Ask(req, "department", jev.Choice(
		"Which team should handle this?",
		jev.Options[Dept]("billing", "technical"),
	))

	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		fmt.Println(err)
		return
	}

	answer, err := dept.Answer(res)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%s (%.2f confidence)\n", answer.Choice, answer.Confidence)
	// Output:
	// technical (0.82 confidence)
}

// ExampleAsk sends several differently typed questions in one Evaluate call
// and reads them back through typed keys, with no type assertion at the call
// site.
func ExampleAsk() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{
			"department":{"type":"choice","choice":"technical","probabilities":{"technical":0.9,"billing":0.1},"confidence":0.9},
			"severity":{"type":"score","score":2,"confidence":0.75,"probabilities":{"0":0.05,"1":0.15,"2":0.8},"legend":{"0":"cosmetic","1":"degraded","2":"blocking"}},
			"is_urgent":{"type":"noul","noul":0.93}
		}}`)
	}))
	defer srv.Close()

	client, err := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(srv.URL))
	if err != nil {
		fmt.Println(err)
		return
	}

	type Dept string
	type Severity int

	req := &jev.Request{State: "Our integration returns 500 on every request."}
	dept := jev.Ask(req, "department", jev.Choice("Which team?", jev.Options[Dept]("billing", "technical")))
	severity := jev.Ask(req, "severity", jev.Score[Severity]("How severe?", "cosmetic", "degraded", "blocking"))
	urgent := jev.Ask(req, "is_urgent", jev.Noul("Is this urgent?"))

	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		fmt.Println(err)
		return
	}

	d, err := dept.Answer(res)
	if err != nil {
		fmt.Println(err)
		return
	}
	s, err := severity.Answer(res)
	if err != nil {
		fmt.Println(err)
		return
	}
	u, err := urgent.Answer(res)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("department=%s severity=%v urgent=%v\n", d.Choice, s.Legend[s.Nearest()], u.Yes(0.5))
	// Output:
	// department=technical severity=blocking urgent=true
}

func ExampleClient_ListModels() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[
			{"name":"jev-latest","description":"Latest stable model","release_date":"2026-01-01"},
			{"name":"jev-preview","description":"Preview model","release_date":"2026-03-01"}
		]}`)
	}))
	defer srv.Close()

	client, err := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(srv.URL))
	if err != nil {
		fmt.Println(err)
		return
	}

	models, err := client.ListModels(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range models {
		fmt.Println(m.Name, m.ReleaseDate)
	}
	// Output:
	// jev-latest 2026-01-01
	// jev-preview 2026-03-01
}

// Example_confidenceGating follows TypeSafe's recommended usage: act on the
// answer only above a threshold set by the cost of a wrong decision, and
// fall back to a human otherwise.
func Example_confidenceGating() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{
			"department":{"type":"choice","choice":"technical","probabilities":{"technical":0.58,"billing":0.42},"confidence":0.58}
		}}`)
	}))
	defer srv.Close()

	client, err := jev.New(jev.WithAPIKey("test"), jev.WithBaseURL(srv.URL))
	if err != nil {
		fmt.Println(err)
		return
	}

	type Dept string

	req := &jev.Request{State: "Our integration returns 500 on every request."}
	dept := jev.Ask(req, "department", jev.Choice("Which team?", jev.Options[Dept]("billing", "technical")))

	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		fmt.Println(err)
		return
	}

	answer, err := dept.Answer(res)
	if err != nil {
		fmt.Println(err)
		return
	}

	const confidenceThreshold = 0.7
	if answer.Confidence < confidenceThreshold {
		fmt.Println("route to human review:", answer.Ranked())
	} else {
		fmt.Println("auto-assign to", answer.Choice)
	}
	// Output:
	// route to human review: [technical billing]
}
