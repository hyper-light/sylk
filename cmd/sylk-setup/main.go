package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

func main() {
	check := flag.Bool("check", false, "Check if libraries are installed")
	help := flag.Bool("help", false, "Show setup instructions")
	flag.Parse()

	if *help {
		fmt.Println(embedder.SetupInstructions())
		return
	}

	if *check {
		result, err := embedder.CheckSetup()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Library directory: %s\n", result.LibDir)
		fmt.Printf("Tokenizers: %s\n", status(result.TokenizersPath != ""))
		fmt.Printf("ONNX Runtime: %s\n", status(result.ONNXRuntimePath != ""))
		fmt.Printf("Ready: %s\n", status(result.Ready))

		if result.Ready {
			fmt.Println("\nBuild command:")
			fmt.Printf("  CGO_LDFLAGS=\"%s\" go build -tags ORT ./...\n", result.CGOLDFlags)
			fmt.Println("\nRun command:")
			fmt.Printf("  LD_LIBRARY_PATH=%s ./your-binary\n", result.LDLibraryPath)
		}
		return
	}

	result, err := embedder.Setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nTo build with ONNX support:")
	fmt.Printf("  CGO_LDFLAGS=\"%s\" go build -tags ORT ./...\n", result.CGOLDFlags)
	fmt.Println("\nTo run:")
	fmt.Printf("  LD_LIBRARY_PATH=%s ./your-binary\n", result.LDLibraryPath)
}

func status(ok bool) string {
	if ok {
		return "installed"
	}
	return "not installed"
}
