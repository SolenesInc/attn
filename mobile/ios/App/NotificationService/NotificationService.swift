import CryptoKit
import Foundation
import Security
import UserNotifications

struct PushNote: Decodable {
    let home_id: String
    let home_name: String
    let profile_id: String
    let profile_name: String
    let session_id: String
    let title: String
    let kind: String
    let text: String
    let ask_id: String?
}

enum PushKey {
    static let info = Data("attn push v1".utf8)
    static let suite = HPKE.Ciphersuite(kem: .Curve25519_HKDF_SHA256, kdf: .HKDF_SHA256, aead: .chaChaPoly)

    static func noiseSecret() -> Data? {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "AttnPushKeychainGroup") as? String else { return nil }
        let query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: "codes.attn.mobile.push",
            kSecAttrAccount: "noise-secret",
            kSecAttrAccessGroup: group,
            kSecReturnData: true,
            kSecMatchLimit: kSecMatchLimitOne,
        ]
        var found: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &found) == errSecSuccess else { return nil }
        return found as? Data
    }

    static func open(_ userInfo: [AnyHashable: Any]) -> PushNote? {
        guard let encoded = userInfo["attn"] as? String,
              let sealed = Data(base64URL: encoded), sealed.count > 32,
              let secret = noiseSecret(),
              let key = try? Curve25519.KeyAgreement.PrivateKey(rawRepresentation: secret),
              var recipient = try? HPKE.Recipient(privateKey: key, ciphersuite: suite, info: info, encapsulatedKey: sealed.prefix(32)),
              let plaintext = try? recipient.open(sealed.dropFirst(32))
        else { return nil }
        return try? JSONDecoder().decode(PushNote.self, from: plaintext)
    }
}

class NotificationService: UNNotificationServiceExtension {
    private var deliver: ((UNNotificationContent) -> Void)?
    private var fallback: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        let content = (request.content.mutableCopy() as? UNMutableNotificationContent) ?? UNMutableNotificationContent()
        content.userInfo = [:]
        content.title = "attn"
        content.body = "An agent is waiting"
        deliver = contentHandler
        fallback = content
        guard let note = PushKey.open(request.content.userInfo) else { return contentHandler(content) }
        content.title = note.title
        content.subtitle = note.home_name + " · " + note.profile_name
        content.body = Self.body(note)
        content.threadIdentifier = note.home_id + "/" + note.profile_id
        content.userInfo = [
            "home_id": note.home_id,
            "profile_id": note.profile_id,
            "session_id": note.session_id,
            "ask_id": note.ask_id ?? "",
        ]
        contentHandler(content)
    }

    override func serviceExtensionTimeWillExpire() {
        if let deliver, let fallback { deliver(fallback) }
    }

    static func body(_ note: PushNote) -> String {
        let label: String
        switch note.kind {
        case "needs_approval": label = "needs approval"
        case "ask": label = "asks"
        default: label = "waiting"
        }
        return note.text.isEmpty ? label : label + ": " + note.text
    }
}

extension Data {
    init?(base64URL text: String) {
        var base64 = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
        self.init(base64Encoded: base64)
    }
}
