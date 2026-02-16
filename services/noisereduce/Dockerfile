FROM rust:1.85-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*
RUN cargo install cargo-watch --locked
WORKDIR /app
COPY Cargo.toml Cargo.lock* ./
RUN mkdir src && echo 'fn main(){}' > src/main.rs && cargo build --release && rm -rf src target/release/noisereduce target/release/deps/noisereduce-*
EXPOSE 5200
CMD ["cargo", "watch", "-w", "src", "-s", "cargo run --release"]
