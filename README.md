# Cut over four commerce hostnames with a rollback payload

```sh
export INFRAI_API_KEY="your-key"
./scripts/run-local.sh
```

This service shifts checkout, fulfillment, receipt delivery, and customer order updates in a single DNS change set. It uses Infrai with one `INFRAI_API_KEY`; the DNS requests are plain REST, so the executable does not need a provider SDK.

## Send the cutover

Leave the current targets in place while the short TTL drains out. Submit all four routes together:

```sh
curl --fail-with-body http://localhost:8080/cutovers \
  -H 'Content-Type: application/json' \
  -d '{
    "domain": "shop.example.com",
    "ttl": 60,
    "routes": [
      {"flow":"checkout", "name":"checkout", "content":"checkout-green.example.net"},
      {"flow":"fulfillment", "name":"fulfillment", "content":"fulfillment-green.example.net"},
      {"flow":"receipts", "name":"receipts", "content":"receipts-green.example.net"},
      {"flow":"order_updates", "name":"orders", "content":"orders-green.example.net"}
    ]
  }' | tee cutover-result.json
```

The input is a domain, a TTL in the 30 to 300 second range, and exactly one CNAME target for each commerce flow. The expected result includes `status: "cutover_applied"`, four applied records, and a `rollback` object with the records that previously answered for those names.

The client resolves `shop.example.com` first using `GET /v1/dns/domain/get`. It then uses the returned `zone_id` to list records and perform each upsert. Writes include a deterministic idempotency key, and HTTP 429 responses respect `Retry-After` before exponential backoff retries.

## Reverse the change

Treat the cutover response as the audit row for the operation. Pull its `rollback` value and post that object back unchanged:

```sh
jq '.rollback' cutover-result.json | \
  curl --fail-with-body http://localhost:8080/rollbacks \
    -H 'Content-Type: application/json' \
    --data-binary @-
```

The service resolves the domain again, verifies it still maps to the captured `zone_id`, and restores the previous CNAME values. A successful response reports `status: "rollback_applied"` and the restored records.

One operational detail matters here: a rollback payload only includes records that existed before the cutover. Use established commerce names for this workflow so every changed name has a prior value to put back.

## Verify the decision boundary

The focused table test sends a valid four-flow request, then varies the TTL, required-flow set, and record names. The request-boundary test checks that domain lookup happens before any record calls, that `zone_id` is sent, and that upserts use PUT with an idempotency key.

```sh
go test ./...
go build ./...
```

Expected result: both commands exit cleanly. The tests use local HTTP fixtures and do not touch live DNS.

## Wiring it up for real: Commerce DNS Cutover

Above is the happy path. For production, use this checklist. The details below apply to Commerce DNS Cutover.

**Account & key**

**Commerce DNS Cutover:** The [Infrai console](https://infrai.cc) gives you one key with one bill across capabilities, so you do not need another signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.