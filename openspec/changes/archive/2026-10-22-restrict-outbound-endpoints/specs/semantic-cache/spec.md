## MODIFIED Requirements

### Requirement: The embedder is configured on its own

The shadow cache SHALL embed with the local embedder by default, or with an OpenAI-compatible endpoint given by its own base URL, model, and optional bearer key, which it calls at the base URL followed by `/embeddings`. The embedder SHALL NOT depend on the chat upstreams. Its endpoint SHALL follow the same URL and credential-transport restrictions as provider endpoints, and its client SHALL reject redirects without resending its payload or credentials. Its key SHALL never appear in logs or cost events, configuration errors SHALL never echo its URL, and a key without an endpoint embedder SHALL fail startup.

#### Scenario: Endpoint embedder

- **WHEN** the embedder is configured as an OpenAI-compatible endpoint with a model and a key
- **THEN** embeddings are requested at the base URL followed by `/embeddings` with that model and key, whatever the chat upstreams are

#### Scenario: Invalid embedder configuration fails startup

- **WHEN** the embedder endpoint is not an absolute URL, has no model, or a key is set for the local embedder
- **THEN** the gateway fails to start, and the error does not contain the URL

### Requirement: Pending shadow samples have a byte budget

Queued and in-progress shadow samples SHALL share a positive configurable retained-data budget, defaulting to 16 MiB. Samples that exceed available capacity SHALL be dropped and counted without waiting for a worker. Capacity SHALL be returned on processing completion or failed enqueue. Closing the evaluator concurrently with an offer SHALL not panic or retain unprocessed samples.

#### Scenario: Pending prompt bytes exceed capacity

- **WHEN** an offer exceeds the available byte budget even though a queue slot exists
- **THEN** it is dropped immediately and counted

#### Scenario: Processing and shutdown return capacity

- **WHEN** accepted evaluations complete or the evaluator drains during shutdown
- **THEN** their reserved bytes are returned and the index retains no raw prompt
