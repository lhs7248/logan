package com.meituan.logan.baseline;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.parser.*;
import com.meituan.logan.web.dto.WebLogDetailDTO;
import org.junit.Test;
import javax.crypto.*;
import javax.crypto.spec.*;
import java.lang.reflect.*;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.security.interfaces.*;
import java.security.spec.*;
import java.util.*;

/** Synthetic uploads executed against unchanged Java parser and decryptor. */
public class WebProtocolVectorsTest {
 private final ObjectMapper mapper=new ObjectMapper();
 private String encode(String s)throws Exception{return URLEncoder.encode(s,"UTF-8");}
 private Map<String,Object> run(String name,String raw){Map<String,Object> v=new LinkedHashMap<>();v.put("name",name);v.put("raw",raw);try{v.put("output",WebLogParser.parse(raw));}catch(Exception e){v.put("error",true);}return v;}
 @Test public void generate()throws Exception{
  TimeZone originalZone=TimeZone.getDefault();Provider originalBC=Security.getProvider("BC");int originalBCPosition=0;Provider[] originalProviders=Security.getProviders();for(int i=0;i<originalProviders.length;i++)if(originalProviders[i].getName().equals("BC"))originalBCPosition=i+1;try {TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));org.apache.log4j.Logger.getRootLogger().addAppender(new org.apache.log4j.varia.NullAppender());
  List<Map<String,Object>> cases=new ArrayList<>();
  cases.add(run("only-commas",",,,"));cases.add(run("null-version",encode("{\"v\":null,\"l\":\"YQ==\"}")));cases.add(run("html-string",encode(mapper.writeValueAsString(Collections.singletonMap("l",Base64.getEncoder().encodeToString("<tag>&中文".getBytes(StandardCharsets.UTF_8)))))));cases.add(run("v0-default",encode("{\"l\":\"aGVsbG8=\"}")));
  cases.add(run("permissive-unpadded",encode("{\"v\":0,\"l\":\"aGVs!bG8\"}")));
  cases.add(run("padding-stops-decoder",encode("{\"l\":\"YQ==YmI=\"}")));
  cases.add(run("url-safe-base64",encode("{\"l\":\"77-_\"}")));
  cases.add(run("unknown-version",encode("{\"v\":99,\"l\":\"YQ==\"}")));
  cases.add(run("missing-log",encode("{}")));
  cases.add(run("malformed-url","%xx"));cases.add(run("malformed-json",encode("{")));
  cases.add(run("string-version",encode("{\"v\":\"0\",\"l\":\"YQ==\"}")));
  cases.add(run("multiple-trailing",encode("{\"l\":\"YQ==\"}")+","+encode("{\"l\":\"Yg==\"}")+",,"));
  Provider bc=new org.bouncycastle.jce.provider.BouncyCastleProvider(); for(Object prop:new ArrayList<Object>(bc.keySet())) {if(prop.toString().startsWith("Cipher.")||prop.toString().startsWith("Alg.Alias.Cipher."))bc.remove(prop);} Security.insertProviderAt(bc,2);
  Field field=WebLogDecryptHelper.class.getDeclaredField("RSA_PRIVATE_KEY_H5");field.setAccessible(true);
  RSAPrivateCrtKey priv=(RSAPrivateCrtKey)KeyFactory.getInstance("RSA").generatePrivate(new PKCS8EncodedKeySpec(Base64.getDecoder().decode((String)field.get(null))));
  PublicKey pub=KeyFactory.getInstance("RSA").generatePublic(new RSAPublicKeySpec(priv.getModulus(),priv.getPublicExponent()));
  byte[] key="0123456789abcdef".getBytes(StandardCharsets.UTF_8);String iv="fedcba9876543210";
  Cipher rsa=Cipher.getInstance("RSA/ECB/PKCS1Padding");rsa.init(Cipher.ENCRYPT_MODE,pub);String wrapped=Base64.getEncoder().encodeToString(rsa.doFinal(key));
  Cipher aes=Cipher.getInstance("AES/CTR/NoPadding");aes.init(Cipher.ENCRYPT_MODE,new SecretKeySpec(key,"AES"),new IvParameterSpec(iv.getBytes(StandardCharsets.UTF_8)));
  Map<String,Object> env=new LinkedHashMap<>();env.put("v",1);env.put("k",wrapped);env.put("iv",iv);env.put("l",Base64.getEncoder().encodeToString(aes.doFinal("hello中文".getBytes(StandardCharsets.UTF_8))));cases.add(run("v1-rsa-aes-ctr",encode(mapper.writeValueAsString(env))));org.junit.Assert.assertEquals("[\"hello中文\"]",cases.get(cases.size()-1).get("output"));
  env.put("iv","short");cases.add(run("v1-invalid-iv-skipped",encode(mapper.writeValueAsString(env))));env.put("k","broken");cases.add(run("v1-invalid-wrapped-key",encode(mapper.writeValueAsString(env))));
  List<Map<String,Object>> details=new ArrayList<>();
  for(String[] item:new String[][]{{"normal","{\"t\":\"2\",\"c\":\"hello+%E4%B8%AD%E6%96%87%2B\",\"d\":\"0\"}"},{"fallback","{\"t\":\"oops\",\"c\":\"x\",\"d\":\"oops\"}"},{"overflow","{\"t\":\"2147483648\",\"c\":\"x\",\"d\":\"9223372036854775808\"}"},{"numeric","{\"t\":3,\"c\":\"x\",\"d\":1609459200000}"},{"missing","{\"t\":\"1\"}"},{"bad-url","{\"t\":1,\"c\":\"%xx\",\"d\":1}"},{"bad-json","{"}}){Map<String,Object> v=new LinkedHashMap<>();v.put("name",item[0]);String content=mapper.writeValueAsString(Collections.singletonList(item[1]));v.put("content",content);try{List<Map<String,Object>> out=new ArrayList<>();for(WebLogDetailDTO d:WebLogParser.parseWebLogDetail(content,42)){Map<String,Object> o=new LinkedHashMap<>();o.put("taskId",d.getTaskId());o.put("logType",d.getLogType());o.put("content",d.getContent());o.put("logTime",d.getLogTime());o.put("minuteOffset",d.getMinuteOffset());out.add(o);}v.put("output",out);}catch(Exception e){v.put("error",true);}details.add(v);}
  Map<String,Object> root=new LinkedHashMap<>();root.put("evidence","Synthetic vectors executed by unchanged Java WebLogParser and WebLogDecryptHelper; JVM timezone Asia/Shanghai; BC key factories before SunRsaSign; SunJCE ciphers to accept legacy PKCS1 bytes passed as PKCS8; no private key exported");root.put("cases",cases);root.put("details",details);Path out=Paths.get("../contract/captures/java-controller/java-web-vectors.json");Files.createDirectories(out.getParent());mapper.writerWithDefaultPrettyPrinter().writeValue(out.toFile(),root); } finally {TimeZone.setDefault(originalZone);Security.removeProvider("BC");if(originalBC!=null)Security.insertProviderAt(originalBC,originalBCPosition); }
 }
}
