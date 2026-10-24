package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type clickHouse struct {
	base   url.URL
	client *http.Client
}

func newClickHouse(raw string) (*clickHouse, error) {
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("clickhouse url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("clickhouse url %q: scheme must be http or https", base.Redacted())
	}
	if base.Query().Get("database") == "" {
		return nil, fmt.Errorf("clickhouse url %q: name the database with ?database=", base.Redacted())
	}
	return &clickHouse{base: *base, client: &http.Client{}}, nil
}

func (c *clickHouse) exec(ctx context.Context, query string, params map[string]string) error {
	return c.send(ctx, c.requestURL(params, ""), strings.NewReader(query), nil)
}

func (c *clickHouse) rows(ctx context.Context, query string, params map[string]string, each func(json.RawMessage) error) error {
	return c.send(ctx, c.requestURL(params, ""), strings.NewReader(query+" FORMAT JSONEachRow"), func(body io.Reader) error {
		dec := json.NewDecoder(body)
		for {
			var row json.RawMessage
			if err := dec.Decode(&row); err == io.EOF {
				return nil
			} else if err != nil {
				return fmt.Errorf("clickhouse: read row: %w", err)
			}
			if err := each(row); err != nil {
				return err
			}
		}
	})
}

func insert[Row any](ctx context.Context, c *clickHouse, table string, rows []Row) error {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return c.send(ctx, c.requestURL(nil, "INSERT INTO "+table+" FORMAT JSONEachRow"), &body, nil)
}

func (c *clickHouse) requestURL(params map[string]string, query string) string {
	target := c.base
	values := target.Query()
	values.Set("date_time_input_format", "best_effort")
	values.Set("date_time_output_format", "iso")
	values.Set("output_format_json_quote_64bit_integers", "0")
	if query != "" {
		values.Set("query", query)
	}
	for name, value := range params {
		values.Set("param_"+name, value)
	}
	target.RawQuery = values.Encode()
	return target.String()
}

func (c *clickHouse) send(ctx context.Context, target string, body io.Reader, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("clickhouse: %s: %s", resp.Status, bytes.TrimSpace(message))
	}
	if read == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return read(resp.Body)
}
