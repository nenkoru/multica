fn main() {
    let args: Vec<_> = std::env::args().collect();
    assert_eq!(args.len(), 3, "usage: catalog-store-harness baseline|patched NEW_ROOT");
    if args[1] == "failed-write" {
        println!("{}", catalog_store_harness::failed_write_probe(std::path::Path::new(&args[2])));
        return;
    }
    let patched = match args[1].as_str() { "baseline" | "baseline-check" => false, "patched" => true, _ => panic!("unknown mode") };
    let value = catalog_store_harness::probe(std::path::Path::new(&args[2]), patched);
    println!("{}", serde_json::to_string(&value).unwrap());
    if args[1] == "baseline-check" {
        assert!(value["second_delta"].as_u64().unwrap() <= 8192,
            "RED: unchanged scope wrote another complete catalog");
    }
}
