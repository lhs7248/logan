package com.meituan.logan.baseline;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.dto.WebLogDetailDTO;
import com.meituan.logan.web.parser.LoganProtocol;
import com.meituan.logan.web.parser.WebLogParser;
import org.junit.Assume;
import org.junit.Test;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.OutputStream;
import java.lang.reflect.Method;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.List;

import static org.junit.Assert.*;

/** Opt-in actual SDK inputs. No hand-built protocol bytes, provider reordering, or original source changes. */
public class SDKProductionCompatibilityTest {
    private final ObjectMapper mapper = new ObjectMapper();

    private Path fixtures() {
        String configured = System.getenv("LOGAN_SDK_FIXTURE_DIR");
        Assume.assumeTrue("Actual SDK output required: set LOGAN_SDK_FIXTURE_DIR", configured != null && !configured.isEmpty());
        return Paths.get(configured);
    }

    @Test public void nativeSDKMarkerSurvivesOriginalJavaDecoder() throws Exception {
        Path dir = fixtures();
        JsonNode expected = mapper.readTree(dir.resolve("manifest.json").toFile()).get("native");
        byte[] wire = Files.readAllBytes(dir.resolve("native.bin"));
        LoganProtocol decoder = new LoganProtocol(new ByteArrayInputStream(wire), "sdk-production-compatibility.gz");
        // Supply the unchanged reference resources omitted by the standalone baseline POM.
        ClassLoader previous = Thread.currentThread().getContextClassLoader();
        Path resources = Paths.get("../../../Server/src/main/resources").toAbsolutePath().normalize();
        try (URLClassLoader resourceLoader = new URLClassLoader(new URL[]{resources.toUri().toURL()}, previous)) {
            Thread.currentThread().setContextClassLoader(resourceLoader);
            Method initialize = LoganProtocol.class.getDeclaredMethod("initSecureParams");
            initialize.setAccessible(true);
            assertEquals("Original secure.properties must load", Boolean.TRUE, initialize.invoke(decoder));
        } finally {
            Thread.currentThread().setContextClassLoader(previous);
        }
        // Exercise the exact private production decryptor called by process(), without its OSS write.
        Method decrypt = LoganProtocol.class.getDeclaredMethod("decryptAndAppend", OutputStream.class, byte[].class);
        decrypt.setAccessible(true);
        ByteArrayOutputStream compressed = new ByteArrayOutputStream();
        ByteBuffer input = ByteBuffer.wrap(wire);
        while (input.hasRemaining()) {
            assertEquals("SDK frame marker", 1, input.get() & 255);
            int length = input.getInt();
            assertTrue("SDK frame bounds", length > 0 && length <= input.remaining() - 1);
            byte[] encrypted = new byte[length];
            input.get(encrypted);
            assertEquals(Boolean.TRUE, decrypt.invoke(decoder, compressed, encrypted));
            assertEquals("SDK frame terminator", 0, input.get() & 255);
        }
        byte[] plain = LoganProtocol.decompress(compressed.toByteArray());
        Files.write(dir.resolve("java-native-plain.bin"), plain);
        int found = 0;
        for (String line : new String(plain, StandardCharsets.UTF_8).split("\n")) {
            if (line.trim().isEmpty()) continue;
            JsonNode row = mapper.readTree(line);
            if (expected.get("marker").asText().equals(row.path("c").asText())) {
                found++;
                assertEquals(expected.get("type").asInt(), row.path("f").asInt());
                assertEquals(expected.get("timestamp").asLong(), row.path("l").asLong());
            }
        }
        assertEquals("Original Java must recover actual CLogan SDK marker", 1, found);
    }

    @Test public void encryptedWebSDKMarkerSurvivesOriginalJavaParser() throws Exception {
        Path dir = fixtures();
        JsonNode expected = mapper.readTree(dir.resolve("manifest.json").toFile()).get("web");
        JsonNode upload = mapper.readTree(dir.resolve("web-upload.json").toFile());
        // Production static initialization may append BC, exactly as in the original server.
        String parsed = WebLogParser.parse(upload.get("logArray").asText());
        Files.write(dir.resolve("java-web-plain.json"), parsed.getBytes(StandardCharsets.UTF_8));
        List<WebLogDetailDTO> details = WebLogParser.parseWebLogDetail(parsed, 42);
        assertEquals("Original Java must decrypt the actual encrypted SDK report", 1, details.size());
        assertEquals(expected.get("marker").asText(), details.get(0).getContent());
        assertEquals(expected.get("type").asInt(), details.get(0).getLogType());
        assertEquals(42L, details.get(0).getTaskId());
    }
}
