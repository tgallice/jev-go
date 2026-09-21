# Contributing

## Prerequisites

- Go 1.26.
- [`golangci-lint`](https://golangci-lint.run/) for linting.

## Running checks

```
make test    # go test -race ./...
make cover   # coverage profile and function-level report
make lint    # golangci-lint run ./...
make vet     # go vet ./...
make fmt     # checks gofmt, does not rewrite files
make         # fmt + vet + lint + test
```

Run `make` before proposing a change. `gofmt`, `go vet` and `golangci-lint` must be clean, and `make test` must pass.

## Conventions

### Dependencies

Zero production dependencies: the client is built entirely on the standard library. `testify` is the only dependency in the module and is reserved for tests; it must never be imported from a non-test file. Any new dependency, production or test, must be justified in the pull request description.

### Tests

Tests follow the testcase pattern:

- A table of named cases, declared as a slice of an anonymous or named struct.
- The loop variable is named `tc`.
- Each case has a `name` field, in English, passed to `t.Run(tc.name, ...)`.
- Inputs and expectations both live as fields on the case struct, not as separate variables or literals scattered in the test body.
- The body of `t.Run` is the same for every case; behavior differences come from the case's fields, not from branching in the test body.

### Comments

Comments are in English, kept rare, and only explain the "why", an invariant, or a pitfall. They never restate what the code already says. Doc comments on exported symbols are the exception: they are the public documentation and are expected to be complete.

### Style

Run `gofmt`, `go vet` and `golangci-lint` before opening a pull request; none of the three should report anything on changed code.
