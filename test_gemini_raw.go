package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	client, _ := genai.NewClient(context.Background(), &genai.ClientConfig{APIKey: apiKey})

	prompt := "My authentication system seems to be running out of memory slowly. Could you investigate and deploy a patch?"

	resp, _ := client.Models.GenerateContent(context.Background(), "gemini-2.5-pro", genai.Text(prompt), &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Role: "system",
			Parts: []*genai.Part{
				{Text: genai.Ptr("Return a compound action JSON with sub_results.")},
			},
		},
		ResponseMIMEType: "application/json",
	})
	
	textPart := resp.Candidates[0].Content.Parts[0].(genai.Text)
	fmt.Println("Raw LLM output:\n", textPart)
}
