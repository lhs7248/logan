package com.meituan.logan.baseline;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.parser.LoganProtocol;
import org.junit.Test;
import javax.crypto.Cipher;
import javax.crypto.spec.*;
import java.io.*;
import java.lang.reflect.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.zip.GZIPOutputStream;
import static org.junit.Assert.*;

/** Synthetic vectors cross-checked against unchanged Java code; not captured SDK traffic. */
public class NativeProtocolVectorsTest {
 private static final byte[] KEY="0123456789abcdef".getBytes(StandardCharsets.UTF_8);
 private static final byte[] IV="fedcba9876543210".getBytes(StandardCharsets.UTF_8);
 private byte[] gzip(String s)throws Exception{ByteArrayOutputStream b=new ByteArrayOutputStream();try(GZIPOutputStream g=new GZIPOutputStream(b)){g.write(s.getBytes(StandardCharsets.UTF_8));}return b.toByteArray();}
 private Map<String,Object> vector(String name,byte[][] parts,boolean padded,boolean noise)throws Exception{
  ByteArrayOutputStream wire=new ByteArrayOutputStream(),out=new ByteArrayOutputStream();
  LoganProtocol decoder=new LoganProtocol(new ByteArrayInputStream(new byte[0]),"test.log");
  Field key=LoganProtocol.class.getDeclaredField("secretKey");key.setAccessible(true);key.set(decoder,KEY);
  Field iv=LoganProtocol.class.getDeclaredField("iv");iv.setAccessible(true);iv.set(decoder,IV);
  Method method=LoganProtocol.class.getDeclaredMethod("decryptAndAppend",OutputStream.class,byte[].class);method.setAccessible(true);
  if(noise)wire.write(new byte[]{0,2,3});
  for(byte[] part:parts){
   Cipher c=Cipher.getInstance(padded?"AES/CBC/PKCS5Padding":"AES/CBC/NoPadding");c.init(Cipher.ENCRYPT_MODE,new SecretKeySpec(KEY,"AES"),new IvParameterSpec(IV));
   byte[] plain=padded?part:Arrays.copyOf(part,((part.length+15)/16)*16);
   byte[] encrypted=c.doFinal(plain);
   wire.write(1);DataOutputStream d=new DataOutputStream(wire);d.writeInt(encrypted.length);d.write(encrypted);d.write(0);
   assertTrue(name,(Boolean)method.invoke(decoder,out,encrypted));
  }
  Map<String,Object> v=new LinkedHashMap<>();v.put("name",name);v.put("wire",Base64.getEncoder().encodeToString(wire.toByteArray()));v.put("compressed",Base64.getEncoder().encodeToString(out.toByteArray()));v.put("plain",Base64.getEncoder().encodeToString(LoganProtocol.decompress(out.toByteArray())));return v;
 }
 @Test public void generateJavaKnownAnswers()throws Exception{
  org.apache.log4j.Logger.getRootLogger().addAppender(new org.apache.log4j.varia.NullAppender());
  List<Map<String,Object>> cases=new ArrayList<>();
  byte[] a=gzip("{\"c\":\"hello中文\",\"f\":1}\n"),b=gzip("{\"c\":\"second\",\"f\":2}\n");
  cases.add(vector("zero-padded",new byte[][]{a},false,false));
  cases.add(vector("pkcs5-single-block",new byte[][]{"abc".getBytes(StandardCharsets.UTF_8)},true,false));
  byte[] large=new byte[65568];for(int i=0;i<large.length;i++)large[i]=(byte)(i%251);
  cases.add(vector("multi-chunk-frame",new byte[][]{large},false,false));
  cases.add(vector("pkcs5-probe",new byte[][]{a},true,false));
  cases.add(vector("multiple-frames-with-noise",new byte[][]{a,b},false,true));
  cases.add(vector("truncated-gzip",new byte[][]{Arrays.copyOf(a,a.length-6)},true,false));
  cases.add(vector("no-final-newline",new byte[][]{gzip("first\nlast-no-newline")},false,false));
  cases.add(vector("empty-upload",new byte[][]{},false,false));
  byte[] crc=gzip("first\nlast-no-newline");crc[crc.length-8]^=1;cases.add(vector("bad-crc-trims-incomplete-line",new byte[][]{crc},true,false));
  StringBuilder longLine=new StringBuilder("first\n");for(int i=0;i<65540;i++)longLine.append('a');byte[] longGzip=gzip(longLine.toString());longGzip[longGzip.length-8]^=1;cases.add(vector("cross-buffer-last-newline",new byte[][]{longGzip},true,false));
  byte[] brokenHeader=new byte[]{31,(byte)139,8};cases.add(vector("damaged-next-member-header",new byte[][]{a,brokenHeader},true,false));
  Cipher probe=Cipher.getInstance("AES/CBC/NoPadding");probe.init(Cipher.ENCRYPT_MODE,new SecretKeySpec(KEY,"AES"),new IvParameterSpec(IV));byte[] validPad=new byte[16];validPad[15]=1;byte[] last=probe.doFinal(validPad),bad=new byte[32];System.arraycopy(last,0,bad,16,16);
  LoganProtocol reject=new LoganProtocol(new ByteArrayInputStream(new byte[0]),"test.log");Field kf=LoganProtocol.class.getDeclaredField("secretKey");kf.setAccessible(true);kf.set(reject,KEY);Field vf=LoganProtocol.class.getDeclaredField("iv");vf.setAccessible(true);vf.set(reject,IV);Method dm=LoganProtocol.class.getDeclaredMethod("decryptAndAppend",OutputStream.class,byte[].class);dm.setAccessible(true);assertFalse((Boolean)dm.invoke(reject,new ByteArrayOutputStream(),bad));
  ByteArrayOutputStream badWire=new ByteArrayOutputStream();DataOutputStream bd=new DataOutputStream(badWire);bd.writeByte(1);bd.writeInt(32);bd.write(bad);Map<String,Object> rejected=new LinkedHashMap<>();rejected.put("name","valid-probe-invalid-full-padding");rejected.put("wire",Base64.getEncoder().encodeToString(badWire.toByteArray()));rejected.put("expected_error",true);cases.add(rejected);
  Map<String,Object> root=new LinkedHashMap<>();root.put("evidence","unchanged Java decryptAndAppend/decompress; synthetic test-only key");root.put("key_hex","30313233343536373839616263646566");root.put("iv_hex","66656463626139383736353433323130");root.put("cases",cases);
  Path out=Paths.get("../contract/captures/java-controller/java-native-vectors.json");Files.createDirectories(out.getParent());new ObjectMapper().writerWithDefaultPrettyPrinter().writeValue(out.toFile(),root);
 }
}
