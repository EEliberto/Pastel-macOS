import Foundation

@main
struct IPAMetadataTests {
    static func main() throws {
        let account = "test@example.invalid"
        let cases: [([String: Any], String)] = [
            (["appleId": account], account),
            (["apple-id": account], account),
            (["userName": account], account),
            (["com.apple.iTunesStore.downloadInfo": ["accountInfo": ["AppleID": account]]], account),
            (["appleId": "  ", "apple-id": " \(account)\n"], account),
            (["appleId": 123, "userName": account], account),
            (["appleId": "first@example.invalid", "apple-id": account], "first@example.invalid"),
            ([:], "")
        ]
        for format in [PropertyListSerialization.PropertyListFormat.xml, .binary] {
            for (metadata, expected) in cases {
                let data = try PropertyListSerialization.data(fromPropertyList: metadata, format: format, options: 0)
                let parsed = try PropertyListSerialization.propertyList(from: data, format: nil) as! [String: Any]
                precondition(IPAMetadata.appleAccount(in: parsed) == expected, "Account metadata fallback failed")
            }
        }
        print("16 account metadata cases passed (XML and binary).")
    }
}
