use std::net::SocketAddr;

use axum::{Router, body::Bytes, http::StatusCode, response::IntoResponse, routing::{get, post}};
use nnnoiseless::DenoiseState;

const FRAME_SIZE: usize = DenoiseState::FRAME_SIZE; // 480 samples (48kHz)

fn denoise_48k(samples: &[f32]) -> Vec<f32> {
    let mut state = DenoiseState::new();
    let mut out = Vec::with_capacity(samples.len());
    let mut frame_out = [0.0f32; FRAME_SIZE];

    let chunks = samples.chunks(FRAME_SIZE);
    for chunk in chunks {
        let input = if chunk.len() < FRAME_SIZE {
            let mut padded = [0.0f32; FRAME_SIZE];
            padded[..chunk.len()].copy_from_slice(chunk);
            padded
        } else {
            let mut arr = [0.0f32; FRAME_SIZE];
            arr.copy_from_slice(chunk);
            arr
        };
        state.process_frame(&mut frame_out, &input);
        let take = chunk.len().min(FRAME_SIZE);
        out.extend_from_slice(&frame_out[..take]);
    }
    out
}

fn upsample_3x(samples: &[f32]) -> Vec<f32> {
    let mut out = Vec::with_capacity(samples.len() * 3);
    samples.windows(2).for_each(|w| {
        out.push(w[0]);
        out.push(w[0] * (2.0 / 3.0) + w[1] * (1.0 / 3.0));
        out.push(w[0] * (1.0 / 3.0) + w[1] * (2.0 / 3.0));
    });
    if let Some(&last) = samples.last() {
        out.push(last);
        out.push(last);
        out.push(last);
    }
    out
}

fn downsample_3x(samples: &[f32]) -> Vec<f32> {
    samples.iter().step_by(3).copied().collect()
}

fn denoise_16k(samples: &[f32]) -> Vec<f32> {
    let up = upsample_3x(samples);
    let denoised = denoise_48k(&up);
    downsample_3x(&denoised)
}

async fn handle_denoise(body: Bytes) -> impl IntoResponse {
    if body.len() % 4 != 0 {
        return (StatusCode::BAD_REQUEST, "body must be float32 LE samples").into_response();
    }

    let samples: Vec<f32> = body
        .chunks_exact(4)
        .map(|c| f32::from_le_bytes([c[0], c[1], c[2], c[3]]))
        .collect();

    let denoised = tokio::task::spawn_blocking(move || denoise_16k(&samples))
        .await
        .unwrap_or_default();

    let bytes: Vec<u8> = denoised.iter().flat_map(|s| s.to_le_bytes()).collect();
    (StatusCode::OK, bytes).into_response()
}

async fn handle_health() -> &'static str {
    "ok"
}

#[tokio::main]
async fn main() {
    let app = Router::new()
        .route("/denoise", post(handle_denoise))
        .route("/health", get(handle_health));

    let addr = SocketAddr::from(([0, 0, 0, 0], 5200));
    eprintln!("noisereduce listening on {addr}");

    let listener = tokio::net::TcpListener::bind(addr).await.expect("bind failed");
    axum::serve(listener, app).await.expect("server failed");
}
