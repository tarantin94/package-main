# First stage: build the application
FROM golang:1.21-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o pipeline .

# Second stage: create minimal runtime image
FROM alpine:latest

# Install ca-certificates for HTTPS requests if needed
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -s /bin/sh appuser

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/pipeline .

# Change ownership to appuser
RUN chown appuser:appuser pipeline

# Switch to non-root user
USER appuser

# Expose port if your app needs it (optional)
# EXPOSE 8080

# Command to run the application
CMD ["./pipeline"]
