FROM rust:1.84-slim AS build
WORKDIR /app
COPY Cargo.toml Cargo.lock* ./
COPY src/ src/
RUN cargo build --release

FROM debian:bookworm-slim
COPY --from=build /app/target/release/noisereduce /usr/local/bin/noisereduce
EXPOSE 5200
CMD ["noisereduce"]
