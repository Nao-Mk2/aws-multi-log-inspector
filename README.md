# aws-multi-log-inspector

Console app to fetch AWS CloudWatch Logs entries across multiple log groups tied to a given string and print them to stdout.

## Requirements

- Go 1.25+
- AWS credentials with `logs:FilterLogEvents` permission on the target log groups
- AWS region configured (env `AWS_REGION`, profile, or other default sources)

## Install

```
go build ./cmd/aws-multi-log-inspector
```

## Usage

```
aws-multi-log-inspector \
  --filter-pattern <pattern> \
  [--groups g1,g2] \
  [--region ap-northeast-1] \
  --profile your-profile \
  [--start RFC3339] [--end RFC3339] \
  [--extract name=jmespath --next-filter jmes-or-literal] [--pretty]
```

- `--groups`: Comma-separated CloudWatch Log Group names. Alternatively set env `LOG_GROUP_NAMES`.
- `--region`: AWS region (optional). Falls back to AWS SDK defaults if omitted.
- `--profile`: AWS shared config profile. If omitted, the app uses env `AWS_PROFILE`. One of them is required.
- `--filter-pattern`: Search pattern (required). See [Filter and Pattern Syntax](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/FilterAndPatternSyntax.html).
- `--start`/`--end`: Override the time window in RFC3339. If both omitted, last 24h is used. If only `--start` is set, the end is "now". If only `--end` is set, the start is `end-24h`.
- `--extract`: Extract a value from the first search results using JMESPath: `name=path`. For non-JSON messages, the raw text is available as `message`.
- `--next-filter`: Build a second filter using JMESPath evaluated against `{ "value": <extracted> }`, or treat the argument as a literal if not valid JMESPath. You can also embed the extracted value via `{{name}}`, which will be JSON-quoted safely before evaluation.
- `--pretty`: Pretty-print JSON. Both the first and second search results are output as an indented JSON array of records.

Output format (first search; one line per log event when not using `--pretty`):

```
<RFC3339 timestamp> <log-group>/<log-stream> <message>
```

If `--pretty` is set, the first search results are output as an indented JSON array (same as the second search).

## Notes

- Implementation uses `FilterLogEvents` per group and merges results sorted by timestamp.
- The default search window is the last 24 hours; it can be overridden with `--start`/`--end`.
 - Output events are sorted chronologically (ascending by timestamp).
 - With `--pretty`, JSON messages in the first search are pretty-printed; otherwise they are shown as raw strings.
- If no matching events are found and `--pretty` is not set, the tool prints: `No logs found for the given pattern "<pattern>" in the last 24h.` and exits successfully. With `--pretty`, an empty JSON output is emitted.

## Two-Phase Search (Extract and Re-search)

Examples:

1) Extract a user ID from the first search, then use it for a second search across groups:

```
--filter-pattern "ERROR" \
--extract "userID=user.id" \
--next-filter "userId={{userID}}"
```

2) Extract a substring from non-JSON messages and build an `@message` equality filter for the second search:

```
--filter-pattern "WARN" \
--extract "value=message" \
--next-filter "join('', ['@message = \"', {{value}}, '\"'])" \
--pretty
```

The second search results are output as JSON (use `--pretty` for indented output). The first search uses the same JSON format when `--pretty` is enabled.
