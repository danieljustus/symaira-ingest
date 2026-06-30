package paperless

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv("PAPERLESS_URL")
	}
	if token == "" {
		token = os.Getenv("PAPERLESS_TOKEN")
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(url string, result any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) ListDocuments(since time.Time, filters ...string) ([]Document, error) {
	url := c.baseURL + "/api/documents/?format=json&ordering=-created_date"
	if !since.IsZero() {
		url += "&created_date__gte=" + since.Format("2006-01-02")
	}
	for _, f := range filters {
		url += "&" + f
	}

	var all []Document
	for url != "" {
		var page listResponse[Document]
		if err := c.doRequest(url, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		url = page.Next
	}
	return all, nil
}

func (c *Client) GetDocument(id int) (*Document, error) {
	url := c.baseURL + "/api/documents/" + strconv.Itoa(id) + "/?format=json"
	var doc Document
	if err := c.doRequest(url, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *Client) DownloadDocument(id int, dst io.Writer) error {
	url := c.baseURL + "/api/documents/" + strconv.Itoa(id) + "/download/"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	_, err = io.Copy(dst, resp.Body)
	return err
}

func (c *Client) ListTags() ([]Tag, error) {
	var all []Tag
	url := c.baseURL + "/api/tags/?format=json"
	for url != "" {
		var page listResponse[Tag]
		if err := c.doRequest(url, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		url = page.Next
	}
	return all, nil
}

func (c *Client) ListCorrespondents() ([]Correspondent, error) {
	var all []Correspondent
	url := c.baseURL + "/api/correspondents/?format=json"
	for url != "" {
		var page listResponse[Correspondent]
		if err := c.doRequest(url, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		url = page.Next
	}
	return all, nil
}

func (c *Client) ListDocumentTypes() ([]DocumentType, error) {
	var all []DocumentType
	url := c.baseURL + "/api/document_types/?format=json"
	for url != "" {
		var page listResponse[DocumentType]
		if err := c.doRequest(url, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		url = page.Next
	}
	return all, nil
}

func (c *Client) ListStoragePaths() ([]StoragePath, error) {
	var all []StoragePath
	url := c.baseURL + "/api/storage_paths/?format=json"
	for url != "" {
		var page listResponse[StoragePath]
		if err := c.doRequest(url, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		url = page.Next
	}
	return all, nil
}
