# cost-telemetry

## MODIFIED Requirements

### Requirement: Completion events carry cost against a versioned price table

When a price table is configured, the cost event of every completion whose provider-reported usage becomes known SHALL carry the request's cost in US dollars — prompt tokens at the served model's input rate plus completion tokens at its output rate — together with the version label of the table that priced it. When a provider answered but its usage never becomes known (a stream without a usage chunk, a client disconnect, a mid-stream failure), the event SHALL carry a cost estimated from the request's reservation — the input estimate at the input rate plus `max_tokens` or the default reservation at the output rate — marked as estimated. Prices SHALL be keyed by physical model and SHALL come from configuration only. With a table configured, every physical model a route resolves to SHALL be priced, and the gateway SHALL refuse to start otherwise. A table SHALL require a version label. Without a table, events SHALL carry no cost fields and the gateway SHALL announce that once at startup.

#### Scenario: Priced completion carries cost and table version

- **WHEN** a buffered completion succeeds with provider-reported usage and its served model is priced
- **THEN** its cost event carries the cost computed from that usage and the price table version

#### Scenario: Streamed completion is priced at stream end

- **WHEN** a streamed completion delivers its usage in the stream
- **THEN** the cost event emitted at stream end carries the cost computed from that usage

#### Scenario: Unknown usage means no cost fields

- **WHEN** a request ends without any provider answer, such as an upstream error before the first byte
- **THEN** its cost event carries no cost fields

#### Scenario: Answered request without usage carries an estimated cost

- **WHEN** a provider answered but no usage arrived, as in a stream without a usage chunk or a client disconnect mid-stream
- **THEN** the cost event carries the reservation's price, the table version, and the estimated mark

#### Scenario: No price table means no cost fields and one startup notice

- **WHEN** the gateway starts without a price table
- **THEN** it logs one notice that cost events carry no cost fields, and no cost event carries them

#### Scenario: Route model without a price is a startup error

- **WHEN** a price table is configured and a route resolves to a physical model the table does not price
- **THEN** the gateway fails to start with a configuration error naming the model

#### Scenario: Version required with a table

- **WHEN** a price table is configured without a version label, or a version label without a table
- **THEN** the gateway fails to start with a configuration error
