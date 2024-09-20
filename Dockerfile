# Use the official Golang image as a build stage
FROM golang:1.23 AS builder

# Set the Current Working Directory inside the container
RUN mkdir /app
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -o qingping_exporter ./cmd/...

# Start a new stage from scratch
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the Pre-built binary file from the previous stage
COPY --from=builder /app/qingping_exporter .

# Expose port (replace with your application's port)
EXPOSE 10803

# Command to run the executable
CMD ["./qingping_exporter run"]
