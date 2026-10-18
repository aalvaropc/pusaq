# semantic-cache

## ADDED Requirements

### Requirement: The embedder is configured on its own

The shadow cache SHALL embed with the local embedder by default, or with an OpenAI-compatible endpoint given by its own base URL, model, and optional bearer key, which it calls at the base URL followed by `/embeddings`. The embedder SHALL NOT depend on the chat upstreams. Its key SHALL never appear in logs or cost events, configuration errors SHALL never echo its URL, and a key without an endpoint embedder SHALL fail startup.

#### Scenario: Endpoint embedder

- **WHEN** the embedder is configured as an OpenAI-compatible endpoint with a model and a key
- **THEN** embeddings are requested at the base URL followed by `/embeddings` with that model and key, whatever the chat upstreams are

#### Scenario: Invalid embedder configuration fails startup

- **WHEN** the embedder endpoint is not an absolute URL, has no model, or a key is set for the local embedder
- **THEN** the gateway fails to start, and the error does not contain the URL
