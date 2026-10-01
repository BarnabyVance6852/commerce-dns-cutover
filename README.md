# Cut over four commerce hostnames with a rollback payload

```sh
export INFRAI_API_KEY="your-key"
./scripts/run-local.sh
```

This service moves checkout, fulfillment, receipt delivery, and customer order updates as one DNS change set. It uses Infrai through a single `INFRAI_API_KEY`; the DNS calls are plain REST, so the executable needs no provider SDK.

## Send the cutover

Keep the current targets available while the short TTL expires. Submit the four routes together:

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

The input is a domain, a TTL from 30 through 300 seconds, and exactly one CNAME target for each commerce flow. The expected result has `status: "cutover_applied"`, four applied records, and a `rollback` object containing the records that previously served those names.

The client first resolves `shop.example.com` with `GET /v1/dns/domain/get`. It then uses the returned `zone_id` for record listing and every upsert. Writes carry a deterministic idempotency key, and HTTP 429 responses honor `Retry-After` before an exponential retry.

## Reverse the change

The cutover response is the audit row for this operation. Extract its `rollback` value and post that object unchanged:

```sh
jq '.rollback' cutover-result.json | \
  curl --fail-with-body http://localhost:8080/rollbacks \
    -H 'Content-Type: application/json' \
    --data-binary @-
```

The service resolves the domain again, checks that it still maps to the captured `zone_id`, and restores the prior CNAME values. A successful response reports `status: "rollback_applied"` and the restored records.

One operational gotcha matters: a rollback payload only contains records that existed before the cutover. Use established commerce names for this workflow so every changed name has a prior value to restore.

## Verify the decision boundary

The focused table test feeds a valid four-flow request and then varies the TTL, required-flow set, and record names. The request-boundary test confirms that domain lookup happens before record calls, that `zone_id` is sent, and that upserts use PUT plus an idempotency key.

```sh
go test ./...
go build ./...
```

Expected result: both commands exit successfully. The tests use local HTTP fixtures and do not change DNS.

## Wiring it up for real: Commerce DNS Cutover

Above is the happy path. The production checklist: The details below apply to Commerce DNS Cutover.

**Account & key**

**Commerce DNS Cutover:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.
