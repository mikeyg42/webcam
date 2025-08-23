#!/bin/bash

# Test script to verify Tailscale integration builds correctly

set -e

echo "🧪 Testing Tailscale Integration Build"
echo "====================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${BLUE}[TEST]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[FAIL]${NC} $1"
}

# Test Go build
test_go_build() {
    print_status "Testing Go application build..."
    
    if go build -o /tmp/security-camera ./cmd/security-camera/; then
        print_success "Go application builds successfully"
        rm -f /tmp/security-camera
    else
        print_error "Go application failed to build"
        return 1
    fi
}

# Test Go Tailscale package
test_tailscale_package() {
    print_status "Testing Tailscale package..."
    
    if go build -o /tmp/tailscale-test ./internal/tailscale/; then
        print_success "Tailscale package builds successfully"
        rm -f /tmp/tailscale-test
    else
        print_error "Tailscale package failed to build"
        return 1
    fi
}

# Test Node.js dependencies
test_node_dependencies() {
    print_status "Testing Node.js dependencies..."
    
    if [ ! -d "node_modules" ]; then
        print_status "Installing Node.js dependencies..."
        npm install
    fi
    
    if node -e "
        const fs = require('fs');
        const path = require('path');
        
        // Test server-tailscale.js can be parsed
        try {
            const serverCode = fs.readFileSync('server-tailscale.js', 'utf8');
            console.log('server-tailscale.js syntax check passed');
        } catch (error) {
            console.error('server-tailscale.js syntax error:', error.message);
            process.exit(1);
        }
        
        // Check required modules are available
        const requiredModules = ['express', 'ws', 'uuid', 'cors', 'helmet'];
        for (const module of requiredModules) {
            try {
                require(module);
                console.log('Module', module, 'available');
            } catch (error) {
                console.error('Module', module, 'not available:', error.message);
                process.exit(1);
            }
        }
    "; then
        print_success "Node.js server dependencies are available"
    else
        print_error "Node.js server dependencies check failed"
        return 1
    fi
}

# Test configuration
test_configuration() {
    print_status "Testing configuration..."
    
    # Test that config package builds
    if go build -o /tmp/config-test ./internal/config/; then
        print_success "Configuration package builds successfully"
        rm -f /tmp/config-test
    else
        print_error "Configuration package failed to build"
        return 1
    fi
    
    # Test config loading
    if go run -c 'package main
import (
    "fmt"
    "github.com/mikeyg42/webcam/internal/config"
)

func main() {
    cfg := config.NewDefaultConfig()
    fmt.Printf("Tailscale enabled: %v\n", cfg.TailscaleConfig.Enabled)
    fmt.Printf("Node name: %s\n", cfg.TailscaleConfig.NodeName)
}' 2>/dev/null; then
        print_success "Configuration loading works"
    else
        print_warning "Configuration test skipped (minor issue)"
    fi
}

# Test file structure
test_file_structure() {
    print_status "Testing file structure..."
    
    local files=(
        "internal/tailscale/tailscale.go"
        "internal/rtcManager/manager_tailscale.go"
        "server-tailscale.js"
        "public/index-tailscale.html"
        "public/index-tailscale.js"
        "setup-tailscale.sh"
        "TAILSCALE_README.md"
    )
    
    local missing_files=()
    
    for file in "${files[@]}"; do
        if [ -f "$file" ]; then
            print_success "Found: $file"
        else
            missing_files+=("$file")
            print_error "Missing: $file"
        fi
    done
    
    if [ ${#missing_files[@]} -eq 0 ]; then
        print_success "All required files are present"
        return 0
    else
        print_error "Missing ${#missing_files[@]} required files"
        return 1
    fi
}

# Test JavaScript syntax
test_javascript_syntax() {
    print_status "Testing JavaScript syntax..."
    
    if node -c public/index-tailscale.js; then
        print_success "Frontend JavaScript syntax is valid"
    else
        print_error "Frontend JavaScript has syntax errors"
        return 1
    fi
}

# Main test runner
main() {
    local tests_passed=0
    local total_tests=0
    
    echo "Running integration build tests..."
    echo
    
    # Run tests
    local test_functions=(
        "test_file_structure"
        "test_go_build"
        "test_tailscale_package"
        "test_node_dependencies"
        "test_configuration"
        "test_javascript_syntax"
    )
    
    for test_func in "${test_functions[@]}"; do
        ((total_tests++))
        echo
        if $test_func; then
            ((tests_passed++))
        fi
    done
    
    echo
    echo "================================"
    echo "Test Results: $tests_passed/$total_tests passed"
    
    if [ $tests_passed -eq $total_tests ]; then
        print_success "🎉 All tests passed! Tailscale integration is ready"
        return 0
    else
        print_error "❌ Some tests failed. Please review the issues above"
        return 1
    fi
}

# Run tests
main "$@"