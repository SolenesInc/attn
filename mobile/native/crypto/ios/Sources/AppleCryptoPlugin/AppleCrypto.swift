import CryptoKit
import Foundation

enum AppleCrypto {
    struct Failure: Error, CustomStringConvertible {
        let description: String
    }

    static func x25519PublicKey(privateKey: Data) throws -> Data {
        try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey).publicKey.rawRepresentation
    }

    static func x25519SharedSecret(privateKey: Data, publicKey: Data) throws -> Data {
        let secret = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey)
            .sharedSecretFromKeyAgreement(with: Curve25519.KeyAgreement.PublicKey(rawRepresentation: publicKey))
        return secret.withUnsafeBytes { Data($0) }
    }

    static func seal(key: Data, nonce: Data, ad: Data, plaintext: Data) throws -> Data {
        let box = try ChaChaPoly.seal(
            plaintext, using: SymmetricKey(data: key), nonce: ChaChaPoly.Nonce(data: nonce), authenticating: ad)
        return box.ciphertext + box.tag
    }

    static func open(key: Data, nonce: Data, ad: Data, sealed: Data) throws -> Data {
        guard sealed.count >= 16 else {
            throw Failure(description: "a sealed message is at least 16 bytes (the tag); got \(sealed.count)")
        }
        let box = try ChaChaPoly.SealedBox(
            nonce: ChaChaPoly.Nonce(data: nonce), ciphertext: sealed.dropLast(16), tag: sealed.suffix(16))
        do {
            return try ChaChaPoly.open(box, using: SymmetricKey(data: key), authenticating: ad)
        } catch CryptoKitError.authenticationFailure {
            throw Failure(description: "the message failed authentication: wrong key, or it was changed in transit")
        }
    }
}
