package storage

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleRSSFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <link>https://example.com</link>
    <description>A test feed</description>
    <item>
      <title>Test Item 1</title>
      <link>https://example.com/item1</link>
      <guid>item1</guid>
      <description>Test description 1</description>
      <pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Test Item 2</title>
      <link>https://example.com/item2</link>
      <guid>item2</guid>
      <description>Test description 2</description>
      <pubDate>Mon, 02 Jan 2024 00:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

const sampleRSSFeedUpdated = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <link>https://example.com</link>
    <description>A test feed</description>
    <item>
      <title>Test Item 3</title>
      <link>https://example.com/item3</link>
      <guid>item3</guid>
      <description>Test description 3</description>
      <pubDate>Mon, 03 Jan 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Test Item 1</title>
      <link>https://example.com/item1</link>
      <guid>item1</guid>
      <description>Test description 1</description>
      <pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Test Item 2</title>
      <link>https://example.com/item2</link>
      <guid>item2</guid>
      <description>Test description 2</description>
      <pubDate>Mon, 02 Jan 2024 00:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

// TestFetchFeedWithETag tests that ETag is properly sent in If-None-Match header
func TestFetchFeedWithETag(t *testing.T) {
	etag := `"test-etag-123"`
	requestReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true

		// Verify the If-None-Match header is sent
		if r.Header.Get("If-None-Match") != etag {
			t.Errorf("Expected If-None-Match header to be %s, got %s", etag, r.Header.Get("If-None-Match"))
		}

		// Return 200 with new ETag
		w.Header().Set("ETag", `"new-etag-456"`)
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		ETag: sql.NullString{
			String: etag,
			Valid:  true,
		},
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if !requestReceived {
		t.Error("Request was not received by test server")
	}

	if result.ETag == nil || *result.ETag != `"new-etag-456"` {
		t.Errorf("Expected new ETag to be \"new-etag-456\", got %v", result.ETag)
	}

	if result.Feed == nil {
		t.Error("Expected feed to be parsed")
	}

	if len(result.Feed.Items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(result.Feed.Items))
	}
}

// TestFetchFeedWithLastModified tests that Last-Modified is properly sent in If-Modified-Since header
func TestFetchFeedWithLastModified(t *testing.T) {
	lastModified := "Mon, 01 Jan 2024 00:00:00 GMT"
	requestReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true

		// Verify the If-Modified-Since header is sent
		if r.Header.Get("If-Modified-Since") != lastModified {
			t.Errorf("Expected If-Modified-Since header to be %s, got %s", lastModified, r.Header.Get("If-Modified-Since"))
		}

		// Return 200 with new Last-Modified
		newLastModified := "Mon, 02 Jan 2024 00:00:00 GMT"
		w.Header().Set("Last-Modified", newLastModified)
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		LastModified: sql.NullString{
			String: lastModified,
			Valid:  true,
		},
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if !requestReceived {
		t.Error("Request was not received by test server")
	}

	if result.LastModified == nil || *result.LastModified != "Mon, 02 Jan 2024 00:00:00 GMT" {
		t.Errorf("Expected new Last-Modified to be Mon, 02 Jan 2024 00:00:00 GMT, got %v", result.LastModified)
	}
}

// TestFetchFeedWith304NotModified tests that 304 responses are handled correctly
func TestFetchFeedWith304NotModified(t *testing.T) {
	etag := `"test-etag-123"`
	lastModified := "Mon, 01 Jan 2024 00:00:00 GMT"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers are sent
		if r.Header.Get("If-None-Match") != etag {
			t.Errorf("Expected If-None-Match header to be %s", etag)
		}
		if r.Header.Get("If-Modified-Since") != lastModified {
			t.Errorf("Expected If-Modified-Since header to be %s", lastModified)
		}

		// Return 304 Not Modified
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		ETag: sql.NullString{
			String: etag,
			Valid:  true,
		},
		LastModified: sql.NullString{
			String: lastModified,
			Valid:  true,
		},
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// 304 should return empty feed
	if result.Feed == nil {
		t.Error("Expected feed to be not nil")
	}

	if len(result.Feed.Items) != 0 {
		t.Errorf("Expected 0 items for 304 response, got %d", len(result.Feed.Items))
	}

	// Cache headers should be preserved
	if result.ETag == nil || *result.ETag != etag {
		t.Errorf("Expected ETag to be preserved as %s, got %v", etag, result.ETag)
	}

	if result.LastModified == nil || *result.LastModified != lastModified {
		t.Errorf("Expected Last-Modified to be preserved as %s, got %v", lastModified, result.LastModified)
	}
}

// TestFetchFeedWithGzipCompression tests that gzip-compressed feeds are handled correctly
func TestFetchFeedWithGzipCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Accept-Encoding header
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if !strings.Contains(acceptEncoding, "gzip") {
			t.Errorf("Expected Accept-Encoding to contain gzip, got %s", acceptEncoding)
		}

		// Return gzipped content
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/rss+xml")

		gz := gzip.NewWriter(w)
		defer gz.Close()
		gz.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result.Feed == nil {
		t.Error("Expected feed to be parsed")
	}

	if len(result.Feed.Items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(result.Feed.Items))
	}
}

// TestFetchFeedWithUserAgent tests that User-Agent is properly set
func TestFetchFeedWithUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent := r.Header.Get("User-Agent")
		if !strings.Contains(userAgent, "RSSBot") {
			t.Errorf("Expected User-Agent to contain RSSBot, got %s", userAgent)
		}

		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

// TestFetchFeedWith429TooManyRequests tests handling of 429 status code
func TestFetchFeedWith429TooManyRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for 429 status code")
	}

	if !strings.Contains(err.Error(), "429") {
		t.Errorf("Expected error to mention 429, got %v", err)
	}

	if !strings.Contains(err.Error(), "60") {
		t.Errorf("Expected error to mention Retry-After value, got %v", err)
	}
}

// TestFetchFeedWith503ServiceUnavailable tests handling of 503 status code
func TestFetchFeedWith503ServiceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for 503 status code")
	}

	if !strings.Contains(err.Error(), "503") {
		t.Errorf("Expected error to mention 503, got %v", err)
	}

	if !strings.Contains(err.Error(), "120") {
		t.Errorf("Expected error to mention Retry-After value, got %v", err)
	}
}

// TestFetchFeedWithNoCacheHeaders tests that feeds without cache headers work correctly
func TestFetchFeedWithNoCacheHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't set any cache headers
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result.Feed == nil {
		t.Error("Expected feed to be parsed")
	}

	// Cache headers should be nil when not provided by server
	if result.ETag != nil {
		t.Error("Expected ETag to be nil when server doesn't provide it")
	}

	if result.LastModified != nil {
		t.Error("Expected Last-Modified to be nil when server doesn't provide it")
	}
}

// TestFetchFeedWithLastEntryFilter tests that items after lastEntry are filtered
func TestFetchFeedWithLastEntryFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeedUpdated))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	// Set lastEntry to item2
	lastEntry := "item2"
	result, err := feed.Check(&lastEntry)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result.Feed == nil {
		t.Error("Expected feed to be parsed")
	}

	// Should only return item3 and item1 (items before item2)
	if len(result.Feed.Items) != 2 {
		t.Errorf("Expected 2 items (before item2), got %d", len(result.Feed.Items))
	}

	// Verify that item2 is not included
	for _, item := range result.Feed.Items {
		if item.GUID == "item2" {
			t.Error("Expected item2 to be filtered out")
		}
	}
}

// TestFetchFeedWithInvalidURL tests error handling for invalid URLs
func TestFetchFeedWithInvalidURL(t *testing.T) {
	feed := Feed{
		Url: "http://invalid-url-that-does-not-exist-12345.com/feed",
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for invalid URL")
	}
}

// TestFetchFeedWithInvalidXML tests error handling for invalid RSS/XML
func TestFetchFeedWithInvalidXML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid xml content"))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for invalid XML")
	}

	if !strings.Contains(err.Error(), "failed to parse feed") {
		t.Errorf("Expected parse error, got %v", err)
	}
}

// TestFetchFeedWith404NotFound tests handling of 404 status code
func TestFetchFeedWith404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for 404 status code")
	}

	if !strings.Contains(err.Error(), "404") {
		t.Errorf("Expected error to mention 404, got %v", err)
	}
}

// TestFetchFeedWithTimeout tests timeout handling
func TestFetchFeedWithTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client timeout
		time.Sleep(35 * time.Second)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected timeout error")
	}
}

// TestFetchFeedBothETagAndLastModified tests that both headers work together
func TestFetchFeedBothETagAndLastModified(t *testing.T) {
	etag := `"test-etag"`
	lastModified := "Mon, 01 Jan 2024 00:00:00 GMT"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify both headers are sent
		if r.Header.Get("If-None-Match") != etag {
			t.Errorf("Expected If-None-Match header to be %s", etag)
		}
		if r.Header.Get("If-Modified-Since") != lastModified {
			t.Errorf("Expected If-Modified-Since header to be %s", lastModified)
		}

		// Return both new headers
		w.Header().Set("ETag", `"new-etag"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2024 00:00:00 GMT")
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		ETag: sql.NullString{
			String: etag,
			Valid:  true,
		},
		LastModified: sql.NullString{
			String: lastModified,
			Valid:  true,
		},
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify both cache headers were updated
	if result.ETag == nil || *result.ETag != `"new-etag"` {
		t.Errorf("Expected ETag to be updated to \"new-etag\", got %v", result.ETag)
	}

	if result.LastModified == nil || *result.LastModified != "Mon, 02 Jan 2024 00:00:00 GMT" {
		t.Errorf("Expected Last-Modified to be updated, got %v", result.LastModified)
	}
}

// BenchmarkFetchFeedWith304 benchmarks the performance of 304 responses
func BenchmarkFetchFeedWith304(b *testing.B) {
	etag := `"test-etag"`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		ETag: sql.NullString{
			String: etag,
			Valid:  true,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := feed.Check(nil)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}

// BenchmarkFetchFeedWithGzip benchmarks the performance of gzipped feeds
func BenchmarkFetchFeedWithGzip(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/rss+xml")

		gz := gzip.NewWriter(w)
		defer gz.Close()
		gz.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := feed.Check(nil)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}

// TestConcurrentFeedFetches tests concurrent feed fetches
func TestConcurrentFeedFetches(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	// Fetch feed concurrently
	const numGoroutines = 10
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := feed.Check(nil)
			errors <- err
		}()
	}

	// Check for errors
	for i := 0; i < numGoroutines; i++ {
		err := <-errors
		if err != nil {
			t.Errorf("Concurrent fetch failed: %v", err)
		}
	}

	if requestCount != numGoroutines {
		t.Errorf("Expected %d requests, got %d", numGoroutines, requestCount)
	}
}

// TestReadResponseBodyWithoutCompression tests reading uncompressed responses
func TestReadResponseBodyWithoutCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	result, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result.Feed == nil {
		t.Error("Expected feed to be parsed")
	}

	if result.Feed.Title != "Test Feed" {
		t.Errorf("Expected feed title 'Test Feed', got %s", result.Feed.Title)
	}
}

// TestFetchFeedWithEmptyETag tests that empty ETag is not sent
func TestFetchFeedWithEmptyETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify that If-None-Match is not sent when ETag is empty
		if r.Header.Get("If-None-Match") != "" {
			t.Error("Expected If-None-Match header to not be sent when ETag is empty")
		}

		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
		ETag: sql.NullString{
			String: "",
			Valid:  true,
		},
	}

	_, err := feed.Check(nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

// TestFetchFeedPreservesNewItemsAfterLastEntry tests filtering logic
func TestFetchFeedPreservesNewItemsAfterLastEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		// Feed has items: item3, item1, item2 (in that order)
		w.Write([]byte(sampleRSSFeedUpdated))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	// Last seen was item1, so we should only get item3
	lastEntry := "item1"
	result, err := feed.Check(&lastEntry)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(result.Feed.Items) != 1 {
		t.Errorf("Expected 1 new item, got %d", len(result.Feed.Items))
	}

	if len(result.Feed.Items) > 0 && result.Feed.Items[0].GUID != "item3" {
		t.Errorf("Expected first item to be item3, got %s", result.Feed.Items[0].GUID)
	}
}

// TestReadResponseBodyGzipError tests error handling when gzip decompression fails
func TestReadResponseBodyGzipError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		// Write invalid gzip data
		w.Write([]byte("not gzipped data"))
	}))
	defer server.Close()

	feed := Feed{
		Url: server.URL,
	}

	_, err := feed.Check(nil)
	if err == nil {
		t.Fatal("Expected error for invalid gzip data")
	}

	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("Expected error to mention gzip, got %v", err)
	}
}

// Example test showing typical usage pattern
func ExampleFeed_Check() {
	// Create a test server that returns an RSS feed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"example-etag"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sampleRSSFeed))
	}))
	defer server.Close()

	// Create a feed
	feed := Feed{
		Url: server.URL,
	}

	// First check - no cache headers
	result, err := feed.Check(nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Items: %d\n", len(result.Feed.Items))
	fmt.Printf("Has ETag: %v\n", result.ETag != nil)
	fmt.Printf("Has Last-Modified: %v\n", result.LastModified != nil)

	// Output:
	// Items: 2
	// Has ETag: true
	// Has Last-Modified: true
}
