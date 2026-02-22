use std::env;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::thread::JoinHandle;

use fetchurl_sdk as fetchurl;
use testcontainers::clients::Cli;
use testcontainers::core::WaitFor;
use testcontainers::images::generic::GenericImage;

fn parse_image(image: &str) -> (String, String) {
    if let Some((name, tag)) = image.rsplit_once(':') {
        if !tag.contains('/') {
            return (name.to_string(), tag.to_string());
        }
    }
    (image.to_string(), "latest".to_string())
}

fn start_upstream_server(content: Vec<u8>) -> (u16, JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind upstream");
    let port = listener.local_addr().unwrap().port();

    let handle = std::thread::spawn(move || {
        if let Ok((mut stream, _)) = listener.accept() {
            let mut buf = [0u8; 8192];
            let mut req = Vec::new();
            loop {
                let n = stream.read(&mut buf).unwrap_or(0);
                if n == 0 {
                    break;
                }
                req.extend_from_slice(&buf[..n]);
                if req.windows(4).any(|w| w == b"\r\n\r\n") {
                    break;
                }
            }
            let req_line = req
                .split(|&b| b == b'\n')
                .next()
                .unwrap_or(&[]);
            let req_line = String::from_utf8_lossy(req_line);
            let path = req_line
                .split_whitespace()
                .nth(1)
                .unwrap_or("");

            if path == "/file" {
                let header = format!(
                    "HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n",
                    content.len()
                );
                let _ = stream.write_all(header.as_bytes());
                let _ = stream.write_all(&content);
            } else {
                let _ = stream.write_all(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n");
            }
        }
    });

    (port, handle)
}

#[test]
fn integration_fetchurl_server() {
    let image = match env::var("FETCHURL_TEST_IMAGE") {
        Ok(v) if !v.trim().is_empty() => v,
        _ => {
            eprintln!("FETCHURL_TEST_IMAGE not set; skipping integration test");
            return;
        }
    };

    let content = b"integration-test".to_vec();
    let hash = {
        use sha2::Digest;
        let mut hasher = sha2::Sha256::new();
        hasher.update(&content);
        format!("{:x}", hasher.finalize())
    };

    let (upstream_port, upstream_handle) = start_upstream_server(content.clone());
    let host_for_container = env::var("FETCHURL_TEST_HOST")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "host.testcontainers.internal".to_string());

    let (name, tag) = parse_image(&image);
    let docker = Cli::default();
    let server_image = GenericImage::new(name, tag)
        .with_cmd(vec!["server"])
        .with_exposed_port(8080)
        .with_wait_for(WaitFor::message_on_stdout("Starting server"));
    let server = docker.run(server_image);

    let old_env = env::var("FETCHURL_SERVER").ok();
    let host_port = server.get_host_port_ipv4(8080);
    env::set_var("FETCHURL_SERVER", format!("\"http://127.0.0.1:{host_port}\""));

    let source_url = format!("http://{host_for_container}:{upstream_port}/file");
    let mut session =
        fetchurl::FetchSession::new("sha256", &hash, &[source_url.as_str()]).unwrap();

    let mut output = Vec::new();
    while let Some(attempt) = session.next_attempt() {
        let mut req = ureq::get(attempt.url());
        for (k, v) in attempt.headers() {
            req = req.set(k, v);
        }
        let resp = req.call();
        if resp.error() {
            continue;
        }

        let mut verifier = session.verifier(&mut output);
        let mut reader = resp.into_reader();
        let mut buf = [0u8; 8192];
        loop {
            let n = reader.read(&mut buf).unwrap_or(0);
            if n == 0 {
                break;
            }
            verifier.write_all(&buf[..n]).unwrap();
        }
        verifier.finish().unwrap();
        session.report_success();
        break;
    }

    if let Some(val) = old_env {
        env::set_var("FETCHURL_SERVER", val);
    } else {
        env::remove_var("FETCHURL_SERVER");
    }

    upstream_handle.join().unwrap();

    assert_eq!(output, content);
    assert!(session.succeeded());
}
