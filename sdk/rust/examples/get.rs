//! Example CLI client for fetchurl, similar to the Go `fetchurl get` command.
//!
//! Usage:
//!   cargo run --example get -- sha256 HASH --url URL1 --url URL2 -o output.tar.gz
//!
//! Set FETCHURL_SERVER to use cache servers:
//!   FETCHURL_SERVER='"http://cache:8080/api/fetchurl"' cargo run --example get -- sha256 HASH --url URL

use std::fs::File;
use std::io::{self, Write};
use std::process;

use clap::Parser;
use fetchurl_sdk as fetchurl;

#[derive(Parser)]
#[command(name = "fetchurl-get", about = "Fetch a file using content-addressable storage")]
struct Cli {
    /// Hash algorithm (sha1, sha256, sha512)
    algo: String,

    /// Expected hash in hex
    hash: String,

    /// Source URLs (can be specified multiple times)
    #[arg(long = "url")]
    urls: Vec<String>,

    /// Output file path (defaults to stdout)
    #[arg(short, long)]
    output: Option<String>,
}

fn main() {
    let cli = Cli::parse();

    let mut session =
        match fetchurl::FetchSession::new(&cli.algo, &cli.hash, &cli.urls) {
            Ok(s) => s,
            Err(e) => {
                eprintln!("error: {e}");
                process::exit(1);
            }
        };

    let mut out: Box<dyn Write> = match cli.output {
        Some(ref path) => match File::create(path) {
            Ok(f) => Box::new(f),
            Err(e) => {
                eprintln!("error: cannot create {path}: {e}");
                process::exit(1);
            }
        },
        None => Box::new(io::stdout()),
    };

    while let Some(attempt) = session.next_attempt() {
        eprintln!("trying: {}", attempt.url());

        let mut req = ureq::get(attempt.url());
        for (key, value) in attempt.headers() {
            req = req.set(key, value);
        }

        let response = match req.call() {
            Ok(r) => r,
            Err(e) => {
                eprintln!("  failed: {e}");
                continue;
            }
        };

        let mut reader = response.into_reader();
        let mut verifier = session.verifier(&mut *out);

        if let Err(e) = io::copy(&mut reader, &mut verifier) {
            eprintln!("  download error: {e}");
            if verifier.bytes_written() > 0 {
                session.report_partial();
                break;
            }
            continue;
        }

        let written = verifier.bytes_written();
        match verifier.finish() {
            Ok(_) => {
                session.report_success();
                break;
            }
            Err(e) => {
                eprintln!("  verification failed: {e}");
                if written > 0 {
                    session.report_partial();
                    break;
                }
            }
        }
    }

    if !session.succeeded() {
        eprintln!("error: failed to fetch from any source");
        if let Some(ref path) = cli.output {
            let _ = std::fs::remove_file(path);
        }
        process::exit(1);
    }
}
