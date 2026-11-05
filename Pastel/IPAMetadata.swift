import Foundation

enum IPAMetadata {
    static func appleAccount(in metadata: [String: Any]) -> String {
        let downloadInfo = metadata["com.apple.iTunesStore.downloadInfo"] as? [String: Any]
        let accountInfo = downloadInfo?["accountInfo"] as? [String: Any]
        for value in [metadata["appleId"], metadata["apple-id"], metadata["userName"], accountInfo?["AppleID"]] {
            if let string = value as? String {
                let account = string.trimmingCharacters(in: .whitespacesAndNewlines)
                if !account.isEmpty { return account }
            }
        }
        return ""
    }
}
