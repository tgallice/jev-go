# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-09-21

### Added

- Three typed question primitives: `Noul` (yes/no), `Choice[T]` (single pick with probability distribution), `Score[L]` / `ScoreMap[L]` (ordered scale with probability-weighted mean).
- Heterogeneous typed batches: `Ask` registers a question on a `Request` and returns a typed `Key[A]`, so a single `Evaluate` call can answer several differently typed questions with no type assertion at the call site.
- Free functions `NoulOf`, `ChoiceOf[T]`, `ScoreOf[L]` to read an answer by key from a `*Result` built without `Ask`.
- `Client` configured through functional options (`WithAPIKey`, `WithBaseURL`, `WithModel`, `WithHTTPClient`, `WithTimeout`, `WithUserAgent`, `WithHeader`, `WithLogger`, `WithRetryPolicy`) and per-call options (`WithCallTimeout`, `WithCallRetry`, `WithCallHeader`).
- Environment variable resolution for API key, base URL and default model (`TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL`, `TYPESAFE_DEFAULT_MODEL`).
- Retry policy (`RetryPolicy`, `DefaultRetryPolicy`, `NoRetry`) with exponential backoff, jitter, and `Retry-After` handling up to a configurable ceiling.
- An error hierarchy with HTTP status sentinels (`ErrBadRequest`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrUnprocessable`, `ErrRateLimited`, `ErrOverloaded`, `ErrServer`) matched through `(*APIError).Is`, plus local validation sentinels (`ErrMissingAPIKey`, `ErrInvalidRequest`, `ErrInvalidResponse`, `ErrAnswerMissing`, `ErrAnswerKind`) and `*TransportError` for network-level failures.
- `Client.ListModels` to list the models advertised by the API.
- Forward compatibility escape hatches: `RawQuestion` for unknown question types, `Request.Extra` for extra top-level request fields, and `Result.RawAnswer` / `Result.Kind` / `Result.RawBody` for reading back what the SDK does not decode.
- Zero production dependencies: the client is built entirely on the standard library.
