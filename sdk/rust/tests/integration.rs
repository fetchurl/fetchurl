use std::env;
use std::fs;
use std::io::{Read, Write};
use std::path::PathBuf;
use std::time::{Duration, Instant};

use fetchurl_sdk as fetchurl;
use testcontainers::clients::Cli;
use testcontainers::core::WaitFor;
use testcontainers::{GenericImage, RunnableImage};

fn parse_image(image: &str) -> (String, String) {
    if let Some((name, tag)) = image.rsplit_once(':') {
        if !tag.contains('/') {
            return (name.to_string(), tag.to_string());
        }
    }
    (image.to_string(), "latest".to_string())
}

fn write_temp_file(content: &[u8]) -> PathBuf {
    let dir = env::temp_dir().join("fetchurl-test-upstream");
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).expect("create temp dir");
    let path = dir.join("file");
    fs::write(&path, content).expect("write file");
    dir
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

    let (name, tag) = parse_image(&image);
    let docker = Cli::default();
    let network_name = format!("fetchurl-test-net-{}", std::process::id());
    let upstream_name = format!("upstream-{}", std::process::id());

    let upstream_dir = write_temp_file(&content);
    let upstream_image = GenericImage::new("python", "3.12-alpine")
        .with_volume(upstream_dir.to_string_lossy().to_string(), "/srv".to_string())
        .with_exposed_port(8000)
        .with_wait_for(WaitFor::seconds(1));
    let upstream = docker.run(
        RunnableImage::from((
            upstream_image,
            vec![
                "python".to_string(),
                "-m".to_string(),
                "http.server".to_string(),
                "8000".to_string(),
                "--bind".to_string(),
                "0.0.0.0".to_string(),
                "--directory".to_string(),
                "/srv".to_string(),
            ],
        ))
        .with_network(network_name.clone())
        .with_container_name(upstream_name.as_str()),
    );

    let server_image = GenericImage::new(name, tag)
        .with_exposed_port(8080)
        .with_wait_for(WaitFor::seconds(1));
    let server = docker.run(
        RunnableImage::from((
            server_image,
            vec!["server".to_string()],
        ))
        .with_network(network_name),
    );

    let old_env = env::var("FETCHURL_SERVER").ok();
    let host_port = server.get_host_port_ipv4(8080);
    unsafe {
        env::set_var(
            "FETCHURL_SERVER",
            format!("\"http://127.0.0.1:{host_port}\""),
        );
    }

    let source_url = format!("http://{upstream_name}:8000/file");
    let mut session =
        fetchurl::FetchSession::new("sha256", &hash, &[source_url.as_str()]).unwrap();
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(Duration::from_secs(5))
        .timeout_read(Duration::from_secs(10))
        .build();

    let deadline = Instant::now() + Duration::from_secs(45);
    let mut output = Vec::new();
    while let Some(attempt) = session.next_attempt() {
        if Instant::now() > deadline {
            panic!("integration test timed out");
        }
        let mut req = agent.get(attempt.url());
        for (k, v) in attempt.headers() {
            req = req.set(k, v);
        }
        let resp = match req.call() {
            Ok(resp) => resp,
            Err(_) => continue,
        };
        if resp.status() != 200 {
            continue;
        }
        let mut verifier = session.verifier(&mut output);
        let mut reader = resp.into_reader();
        let mut buf = [0u8; 8192];
        loop {
            if Instant::now() > deadline {
                panic!("integration test timed out");
            }
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
        unsafe {
            env::set_var("FETCHURL_SERVER", val);
        }
    } else {
        unsafe {
            env::remove_var("FETCHURL_SERVER");
        }
    }

    drop(upstream);

    assert_eq!(output, content);
    assert!(session.succeeded());
}
